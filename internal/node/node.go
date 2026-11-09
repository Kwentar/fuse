package node

import (
	"bytes"
	"context"
	"errors"
	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"math/rand/v2"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fuse/internal/config"
	"fuse/internal/discovery"
	"fuse/internal/identity"
	"fuse/internal/logging"
	"fuse/internal/messaging"
	netstack "fuse/internal/network"
	"fuse/internal/securefs"
	"fuse/internal/storage"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/event"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"golang.org/x/time/rate"
)

type Status struct {
	PeerID           string                `json:"peer_id"`
	State            string                `json:"state"`
	Bootstrap        string                `json:"bootstrap"`
	DHT              string                `json:"dht"`
	Reservation      int                   `json:"reservations"`
	Publication      string                `json:"publication"`
	LastError        string                `json:"last_error,omitempty"`
	Seq              uint64                `json:"seq"`
	Connections      int                   `json:"connections"`
	ContactAddresses []string              `json:"contact_addresses"`
	MaxConnections   int                   `json:"max_connections"`
	MaxStreams       int                   `json:"max_streams"`
	Config           config.Client         `json:"config"`
	ResourceLimits   any                   `json:"resource_limits"`
	Routes           map[string]RouteState `json:"routes,omitempty"`
	Transfers        TransferStats         `json:"transfers"`
}
type senderBudget struct {
	active  int
	limiter *rate.Limiter
	last    time.Time
}
type Node struct {
	routeMu        sync.Mutex
	routes         map[peer.ID]RouteState
	lanPeers       map[peer.ID][]ma.Multiaddr
	routeChecked   map[peer.ID]time.Time
	mdns           mdns.Service
	jobsMu         sync.Mutex
	fileStateMu    sync.Mutex
	receiveFileMu  [64]sync.Mutex
	fileRecords    map[string]FileRecord
	fileMeterMu    sync.Mutex
	fileMeters     map[string]*fileMeter
	fileUploaded   int64
	fileDownloaded int64
	downloadMu     sync.Mutex
	downloadDir    string
	fileJobs       map[string]context.CancelFunc
	dir            string
	fileGate       chan struct{}
	fileQueueWake  chan struct{}
	contactMu      sync.Mutex
	Host           host.Host
	DHT            *dht.IpfsDHT
	Journal        *storage.Journal
	Config         config.Client
	Cache          *discovery.Cache
	key            crypto.PrivKey
	seq            *discovery.Sequence
	validator      discovery.Validator
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	lock           *securefs.Lock
	log            *logging.Log
	mu             sync.Mutex
	status         Status
	budgets        map[peer.ID]*senderBudget
	active         int
	closing        bool
	sendGate       chan struct{}
	onStored       func(peer.ID, messaging.Message)
}

func Open(ctx context.Context, dir string, c config.Client, onStored ...func(peer.ID, messaging.Message)) (n *Node, err error) {
	if err = c.Validate(); err != nil {
		return nil, err
	}
	if err = securefs.CheckFiles(dir, "identity.key", "state.json", "config.json", "peer-cache.json", "messages.jsonl"); err != nil {
		return nil, err
	}
	lock, err := securefs.Acquire(dir)
	if err != nil {
		return nil, err
	}
	n = &Node{Config: c, lock: lock, validator: discovery.Validator{AllowPrivate: c.AllowPrivate}, sendGate: make(chan struct{}, 1), budgets: map[peer.ID]*senderBudget{}}
	n.ctx, n.cancel = context.WithCancel(ctx)
	n.dir = dir
	n.fileGate = make(chan struct{}, 8)
	n.fileQueueWake = make(chan struct{}, 1)
	if err = securefs.EnsureDir(filepath.Join(dir, "files")); err != nil {
		n.cancel()
		lock.Close()
		return nil, err
	}
	n.validator.Conflict = func() { n.setError("CONTACT_SEQ_CONFLICT") }
	if len(onStored) > 0 {
		n.onStored = onStored[0]
	}
	defer func(opened *Node) {
		if err != nil {
			opened.Close()
		}
	}(n)
	if err = n.loadFiles(); err != nil {
		return nil, err
	}
	if n.key, err = identity.Load(dir); err != nil {
		return nil, err
	}
	if n.seq, err = discovery.LoadSequence(dir); err != nil {
		return nil, err
	}
	if n.Cache, err = discovery.LoadCache(dir, n.validator); err != nil {
		return nil, err
	}
	if n.Journal, err = storage.Open(dir); err != nil {
		return nil, err
	}
	if n.log, err = logging.Open(dir); err != nil {
		return nil, err
	}
	bootstrapStrings := append(append([]string{}, c.Bootstrap...), n.Cache.Infrastructure()...)
	bootstrap, err := config.Peers(bootstrapStrings)
	if err != nil {
		return nil, err
	}
	relays, err := config.Peers(c.Relays)
	if err != nil {
		return nil, err
	}
	if len(bootstrap) == 0 || len(relays) == 0 {
		return nil, errors.New("NODE_NOT_READY: configure bootstrap_nodes and relay_nodes in " + filepath.Join(dir, "config.json"))
	}
	listen := c.Direct
	var opts []libp2p.Option
	gate := netstack.AddressGate{AllowPrivate: c.AllowPrivate}
	if c.SmartRouting {
		if len(listen) == 0 {
			listen = []string{"/ip4/0.0.0.0/tcp/0"}
		}
		opts = append(opts, libp2p.EnableHolePunching())
		gate.AllowLAN = n.allowLAN
	}
	if n.Host, err = netstack.NewHostWithPolicy(n.key, listen, relays, false, gate, opts...); err != nil {
		return nil, err
	}
	if n.DHT, err = discovery.NewDHT(n.ctx, n.Host, false, n.validator, bootstrap); err != nil {
		return nil, err
	}
	n.status = Status{PeerID: n.Host.ID().String(), State: "starting", Bootstrap: "connecting", DHT: "starting", Publication: "not_attempted", MaxConnections: 128, MaxStreams: 256, Config: c, ContactAddresses: []string{}}
	n.status.ResourceLimits = netstack.EffectiveLimits(false).ToPartialLimitConfig()
	n.log.Event("NODE_STARTED", n.Host.ID().String())
	n.Host.SetStreamHandler("/p2p-messenger/chat/1.0.0", n.receive)
	n.Host.SetStreamHandler(fileProtocol, n.receiveFile)
	n.Host.SetStreamHandler(fileSessionProtocol, n.receiveFile)
	if c.SmartRouting {
		n.routes = map[peer.ID]RouteState{}
		n.lanPeers = map[peer.ID][]ma.Multiaddr{}
		n.routeChecked = map[peer.ID]time.Time{}
		n.Host.SetStreamHandler(routeProtocol, n.receiveRoutes)
		n.mdns = mdns.NewMdnsService(n.Host, "_p2pm._udp", n)
		if e := n.mdns.Start(); e != nil {
			n.setError("LAN_DISCOVERY_UNAVAILABLE")
		}
	}
	sub, err := n.Host.EventBus().Subscribe([]interface{}{new(event.EvtLocalAddressesUpdated), new(event.EvtAutoRelayAddrsUpdated)})
	if err != nil {
		return nil, err
	}
	if err = n.loadDownloadDir(); err != nil {
		return nil, err
	}
	n.wg.Add(4)
	go func() { defer n.wg.Done(); n.downloadLoop() }()
	go func() { defer n.wg.Done(); n.fileQueueLoop() }()
	go func() { defer n.wg.Done(); n.maintain(bootstrap) }()
	go func() { defer n.wg.Done(); defer sub.Close(); n.publishLoop(sub) }()
	return n, nil
}
func (n *Node) Close() {
	n.mu.Lock()
	n.closing = true
	n.mu.Unlock()
	if n.cancel != nil {
		n.cancel()
	}
	if n.Host != nil {
		n.Host.RemoveStreamHandler("/p2p-messenger/chat/1.0.0")
		n.Host.RemoveStreamHandler(fileProtocol)
		n.Host.RemoveStreamHandler(fileSessionProtocol)
	}
	n.wg.Wait()
	if n.mdns != nil {
		n.mdns.Close()
	}
	if n.DHT != nil {
		n.DHT.Close()
	}
	if n.Host != nil {
		n.Host.Close()
	}
	if n.Journal != nil {
		n.Journal.Close()
	}
	if n.log != nil {
		n.log.Event("NODE_STOPPED", "")
		n.log.Close()
	}
	if n.lock != nil {
		n.lock.Close()
	}
}
func (n *Node) Status() Status {
	n.mu.Lock()
	defer n.mu.Unlock()
	s := n.status
	s.Transfers = n.TransferStats()
	n.routeMu.Lock()
	s.Routes = map[string]RouteState{}
	for id, r := range n.routes {
		if r.Stage == "connected" && len(n.Host.Network().ConnsToPeer(id)) == 0 {
			r = RouteState{Stage: "disconnected"}
		}
		s.Routes[id.String()] = r
	}
	n.routeMu.Unlock()
	s.Connections = len(n.Host.Network().Conns())
	s.ContactAddresses = append([]string{}, s.ContactAddresses...)
	return s
}
func (n *Node) maintain(bootstrap []peer.AddrInfo) {
	backoff := time.Second
	started := time.Now()
	for {
		if n.ctx.Err() != nil {
			return
		}
		ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
		var wg sync.WaitGroup
		connected := make(chan peer.AddrInfo, len(bootstrap))
		for _, p := range bootstrap {
			wg.Add(1)
			go func(p peer.AddrInfo) {
				defer wg.Done()
				if n.Host.Connect(ctx, p) == nil {
					connected <- p
				}
			}(p)
		}
		wg.Wait()
		close(connected)
		good := 0
		for p := range connected {
			good++
			var addresses []string
			for _, a := range p.Addrs {
				addresses = append(addresses, a.String()+"/p2p/"+p.ID.String())
			}
			if e := n.Cache.RememberInfrastructure(addresses); e != nil {
				n.setError("STORAGE_ERROR")
			}
		}
		if good > 0 {
			n.DHT.Bootstrap(ctx)
		}
		cancel()
		n.mu.Lock()
		if good > 0 {
			n.status.Bootstrap = "connected"
		} else {
			n.status.Bootstrap = "unreachable"
		}
		if n.DHT.RoutingTable().Size() > 0 {
			n.status.DHT = "ready"
		} else {
			n.status.DHT = "empty"
		}
		n.updateState(started)
		n.mu.Unlock()
		wait := backoff * time.Duration(800+rand.IntN(201)) / 1000
		if good > 0 {
			wait = 10 * time.Second
			backoff = time.Second
		} else {
			backoff = min(backoff*2, 30*time.Second)
		}
		select {
		case <-n.ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
func (n *Node) updateState(started time.Time) {
	if n.status.DHT == "ready" && len(n.status.ContactAddresses) > 0 && n.status.Publication == "submitted" {
		n.status.State = "online"
	} else if time.Since(started) >= 30*time.Second || n.status.State != "starting" {
		n.status.State = "degraded"
	}
}
func (n *Node) setError(code string) {
	n.mu.Lock()
	n.status.LastError = code
	n.mu.Unlock()
	if n.log != nil {
		n.log.Event(code, "")
	}
}
func (n *Node) contactAddresses() []ma.Multiaddr {
	addresses := append([]ma.Multiaddr{}, n.Host.Addrs()...)
	// Explicit direct test listeners remain useful with forced private reachability.
	if len(n.Config.Direct) > 0 {
		addresses = append(addresses, n.Host.Network().ListenAddresses()...)
	}
	seen := map[string]bool{}
	out := []ma.Multiaddr{}
	for _, a := range addresses {
		if n.validator.Address(a) == nil && !seen[a.String()] {
			out = append(out, a)
			seen[a.String()] = true
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}
func (n *Node) publishLoop(sub event.Subscription) {
	timer := time.NewTimer(time.Second)
	scheduled := time.Now().Add(time.Second)
	defer timer.Stop()
	started := time.Now()
	for {
		select {
		case <-n.ctx.Done():
			return
		case _, ok := <-sub.Out():
			if !ok {
				return
			}
			// Do not indefinitely postpone a publication during an event burst.
			if target := time.Now().Add(750 * time.Millisecond); target.Before(scheduled) {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				scheduled = target
				timer.Reset(time.Until(target))
			}
		case <-timer.C:
			addresses := n.contactAddresses()
			seq, err := n.seq.Next(time.Now())
			state := "error"
			code := ""
			if err == nil {
				var b []byte
				b, err = discovery.Seal(n.key, seq, addresses, n.validator)
				if err == nil {
					ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
					err = n.DHT.PutValue(ctx, discovery.Key(n.Host.ID()), b)
					cancel()
				}
			}
			if err == nil {
				state = "submitted"
			} else {
				code = "PUBLICATION_ERROR"
			}
			n.mu.Lock()
			n.status.Publication = state
			n.status.Seq = seq
			n.status.LastError = code
			n.status.ContactAddresses = []string{}
			relays := map[string]bool{}
			for _, a := range addresses {
				n.status.ContactAddresses = append(n.status.ContactAddresses, a.String())
				if _, e := a.ValueForProtocol(ma.P_CIRCUIT); e == nil {
					id, _ := a.ValueForProtocol(ma.P_P2P)
					relays[id] = true
				}
			}
			n.status.Reservation = len(relays)
			n.updateState(started)
			n.mu.Unlock()
			interval := config.Duration(n.Config.PublishInterval) * time.Duration(800+rand.IntN(401)) / 1000
			if err != nil {
				interval = 5 * time.Second
			}
			timer.Reset(interval)
			scheduled = time.Now().Add(interval)
		}
	}
}

type Found struct {
	PeerID    string   `json:"peer_id"`
	Seq       uint64   `json:"seq"`
	Addresses []string `json:"addresses"`
	Source    string   `json:"source"`
	Result    string   `json:"result"`
}

func (n *Node) Find(ctx context.Context, id peer.ID, force bool) (Found, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Duration(n.Config.DiscoveryTimeout))
	defer cancel()
	cached, exists := n.Cache.Get(id)
	source := "cache"
	data := cached.Envelope
	if force || !exists || time.Since(cached.Seen) > 5*time.Minute {
		b, e := n.DHT.GetValue(ctx, discovery.Key(id))
		if e != nil {
			if !exists {
				if errors.Is(e, context.DeadlineExceeded) || ctx.Err() != nil {
					return Found{}, errors.New("DISCOVERY_TIMEOUT")
				}
				return Found{}, errors.New("CONTACT_NOT_FOUND")
			}
			source = "cache_fallback"
		} else {
			item, e := n.Cache.Put(id, b)
			if e != nil {
				return Found{}, e
			}
			data = item.Envelope
			source = "dht"
			if !bytes.Equal(data, b) {
				source = "cache_newer"
			}
		}
	}
	_, record, e := n.validator.Read(discovery.Key(id), data)
	if e != nil {
		return Found{}, e
	}
	f := Found{PeerID: id.String(), Seq: record.Seq, Addresses: []string{}, Source: source, Result: "verified_contact"}
	for _, a := range record.Addrs {
		f.Addresses = append(f.Addresses, a.String())
	}
	return f, nil
}
func (n *Node) Peers() []map[string]any {
	out := []map[string]any{}
	roles := map[peer.ID]string{}
	for _, s := range n.Config.Bootstrap {
		p, _ := config.Peers([]string{s})
		if len(p) > 0 {
			roles[p[0].ID] = "bootstrap"
		}
	}
	for _, s := range n.Config.Relays {
		p, _ := config.Peers([]string{s})
		if len(p) > 0 {
			roles[p[0].ID] = "relay"
		}
	}
	for _, p := range n.Host.Peerstore().Peers() {
		if p == n.Host.ID() {
			continue
		}
		role := roles[p]
		if role == "" {
			role = "peer"
		}
		out = append(out, map[string]any{"peer_id": p.String(), "role": role, "connected": n.Host.Network().Connectedness(p) == network.Connected || len(n.Host.Network().ConnsToPeer(p)) > 0})
	}
	return out
}
