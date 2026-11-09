package network

import (
	"time"

	libp2p "github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/host/autorelay"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	"github.com/libp2p/go-libp2p/p2p/net/connmgr"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
)

// NewHost enables only TCP, Noise and Yamux. No implicit listener, NAT mapping,
// QUIC, mDNS, hole punching, public IPFS bootstrap or telemetry is configured.
func EffectiveLimits(server bool) rcmgr.ConcreteLimitConfig {
	connections, streams := 128, 256
	if server {
		connections, streams = 256, 1024
	}
	defaults := rcmgr.DefaultLimits
	libp2p.SetDefaultServiceLimits(&defaults)
	return rcmgr.PartialLimitConfig{System: rcmgr.ResourceLimits{
		Conns: rcmgr.LimitVal(connections), ConnsInbound: rcmgr.LimitVal(connections), ConnsOutbound: rcmgr.LimitVal(connections),
		Streams: rcmgr.LimitVal(streams), StreamsInbound: rcmgr.LimitVal(streams), StreamsOutbound: rcmgr.LimitVal(streams),
		Memory: rcmgr.LimitVal64(256 << 20), FD: rcmgr.LimitVal(connections),
	}}.Build(defaults.AutoScale())

}
func NewHost(key crypto.PrivKey, listen []string, relays []peer.AddrInfo, server, privateAddresses bool, extra ...libp2p.Option) (host.Host, error) {
	return NewHostWithPolicy(key, listen, relays, server, AddressGate{AllowPrivate: privateAddresses}, extra...)
}
func NewHostWithPolicy(key crypto.PrivKey, listen []string, relays []peer.AddrInfo, server bool, gate AddressGate, extra ...libp2p.Option) (host.Host, error) {
	connections := 128
	if server {
		connections = 256
	}
	limits := EffectiveLimits(server)
	rm, err := rcmgr.NewResourceManager(rcmgr.NewFixedLimiter(limits))
	if err != nil {
		return nil, err
	}
	cm, err := connmgr.NewConnManager(connections/2, connections*3/4, connmgr.WithGracePeriod(time.Minute))
	if err != nil {
		rm.Close()
		return nil, err
	}
	opts := []libp2p.Option{libp2p.Identity(key), libp2p.NoTransports, libp2p.Transport(tcp.NewTCPTransport),
		libp2p.Security(noise.ID, noise.New), libp2p.Muxer(yamux.ID, yamux.DefaultTransport),
		libp2p.ResourceManager(rm), libp2p.ConnectionManager(cm), libp2p.DisableMetrics(),
		libp2p.ConnectionGater(gate)}
	if len(listen) == 0 {
		opts = append(opts, libp2p.NoListenAddrs)
	} else {
		opts = append(opts, libp2p.ListenAddrStrings(listen...))
	}
	opts = append(opts, libp2p.EnableRelay()) // NoListenAddrs must precede this.
	if server {
		opts = append(opts, libp2p.ForceReachabilityPublic())
	} else {
		opts = append(opts, libp2p.ForceReachabilityPrivate())
		if len(relays) > 0 {
			opts = append(opts, libp2p.EnableAutoRelayWithStaticRelays(relays,
				autorelay.WithMinCandidates(1), autorelay.WithNumRelays(min(2, len(relays))),
				autorelay.WithBootDelay(0), autorelay.WithBackoff(15*time.Second), autorelay.WithMinInterval(5*time.Second)))
		}
	}
	opts = append(opts, extra...)
	h, err := libp2p.New(opts...)
	if err != nil {
		cm.Close()
		rm.Close()
	}
	return h, err
}
