package node

import (
	"context"
	"errors"
	netstack "fuse/internal/network"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/wlynxg/anet"
	"net"
	"time"
)

const routeProtocol = "/p2p-messenger/routes/1.0.0"

type RouteState struct {
	Stage string `json:"stage"`
	Route string `json:"route"`
}
type routeOffer struct {
	Version   int      `json:"version"`
	Addresses []string `json:"addresses"`
}

func ipOf(a ma.Multiaddr) net.IP {
	for _, code := range []int{ma.P_IP4, ma.P_IP6} {
		if s, e := a.ValueForProtocol(code); e == nil {
			return net.ParseIP(s)
		}
	}
	return nil
}
func sameLAN(a ma.Multiaddr) bool {
	p := a.Protocols()
	if len(p) != 2 || p[1].Code != ma.P_TCP {
		return false
	}
	ip := ipOf(a)
	if ip == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	addresses, e := anet.InterfaceAddrs()
	if e != nil {
		return false
	}
	for _, s := range addresses {
		if subnet, ok := s.(*net.IPNet); ok && subnet.IP.IsPrivate() && subnet.Contains(ip) {
			return true
		}
	}
	return false
}
func (n *Node) allowLAN(p peer.ID, a ma.Multiaddr) bool { return sameLAN(a) && n.knownContact(p) }
func (n *Node) HandlePeerFound(p peer.AddrInfo) {
	var addresses []ma.Multiaddr
	for _, a := range p.Addrs {
		if sameLAN(a) && len(addresses) < 8 {
			addresses = append(addresses, a)
		}
	}
	if len(addresses) == 0 {
		return
	}
	n.routeMu.Lock()
	if len(n.lanPeers) < 256 {
		n.lanPeers[p.ID] = addresses
	}
	n.routeMu.Unlock()
}
func (n *Node) setRoute(p peer.ID, stage, route string) {
	n.routeMu.Lock()
	defer n.routeMu.Unlock()
	if n.routes == nil {
		n.routes = map[peer.ID]RouteState{}
	}
	n.routes[p] = RouteState{stage, route}
}
func (n *Node) localOffer() routeOffer {
	o := routeOffer{Version: 1, Addresses: []string{}}
	// libp2p's InterfaceListenAddresses uses net.InterfaceAddrs, which can fail
	// inside Android's app sandbox. Resolve wildcard listeners with anet too.
	interfaces, _ := anet.InterfaceAddrs()
	addresses := resolveRouteListeners(n.Host.Network().ListenAddresses(), interfaces)
	seen := map[string]bool{}
	for _, a := range append(addresses, n.Host.Addrs()...) {
		p := a.Protocols()
		if len(p) == 2 && p[1].Code == ma.P_TCP && (sameLAN(a) || netstack.PublicIP(ipOf(a))) && len(o.Addresses) < 8 && !seen[a.String()] {
			seen[a.String()] = true
			o.Addresses = append(o.Addresses, a.String())
		}
	}
	return o
}

func resolveRouteListeners(listeners []ma.Multiaddr, interfaces []net.Addr) []ma.Multiaddr {
	var ips []ma.Multiaddr
	for _, addr := range interfaces {
		var ip net.IP
		switch a := addr.(type) {
		case *net.IPNet:
			ip = a.IP
		case *net.IPAddr:
			ip = a.IP
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			continue
		}
		if a, e := manet.FromIP(ip); e == nil {
			ips = append(ips, a)
		}
	}
	var out []ma.Multiaddr
	for _, listener := range listeners {
		// The singular function never falls back to the blocked Android net API.
		resolved, e := manet.ResolveUnspecifiedAddress(listener, ips)
		if e == nil {
			out = append(out, resolved...)
		}
	}
	return out
}
func (n *Node) acceptOffer(p peer.ID, o routeOffer) []ma.Multiaddr {
	var public, local []ma.Multiaddr
	if o.Version != 1 || len(o.Addresses) > 8 {
		return nil
	}
	for _, s := range o.Addresses {
		if len(s) > 512 {
			continue
		}
		a, e := ma.NewMultiaddr(s)
		if e != nil {
			continue
		}
		if sameLAN(a) {
			local = append(local, a)
		} else if n.validator.Address(a) == nil && netstack.PublicIP(ipOf(a)) {
			public = append(public, a)
		}
	}
	n.routeMu.Lock()
	if len(local) > 0 {
		n.lanPeers[p] = local
	}
	n.routeMu.Unlock()
	return public
}
func (n *Node) receiveRoutes(s network.Stream) {
	n.mu.Lock()
	if n.closing {
		n.mu.Unlock()
		s.Reset()
		return
	}
	n.wg.Add(1)
	n.mu.Unlock()
	defer n.wg.Done()
	defer s.Close()
	s.SetDeadline(time.Now().Add(5 * time.Second))
	stop := context.AfterFunc(n.ctx, func() { s.Reset() })
	defer stop()
	p := s.Conn().RemotePeer()
	if !n.knownContact(p) {
		s.Reset()
		return
	}
	var o routeOffer
	if wire.Read(s, &o) != nil {
		return
	}
	n.acceptOffer(p, o)
	wire.Write(s, n.localOffer())
}
func (n *Node) tryDirect(ctx context.Context, p peer.ID, addresses []ma.Multiaddr, proto protocol.ID, route string) (network.Stream, error) {
	if len(addresses) == 0 {
		return nil, errors.New("NO_ADDRESS")
	}
	ctx, cancel := context.WithTimeout(network.WithForceDirectDial(ctx, "p2pm-route"), 2*time.Second)
	defer cancel()
	n.Host.Peerstore().ClearAddrs(p)
	if e := n.Host.Connect(ctx, peer.AddrInfo{ID: p, Addrs: addresses}); e != nil {
		return nil, e
	}
	s, e := n.Host.NewStream(ctx, p, proto)
	if e == nil {
		n.setRoute(p, "connected", route)
	}
	return s, e
}
func (n *Node) smartStream(ctx context.Context, p peer.ID, proto protocol.ID) (network.Stream, string, error) {
	// A working direct connection is reused without repeating discovery.
	for _, c := range n.Host.Network().ConnsToPeer(p) {
		if !c.Stat().Limited {
			route := "direct"
			if sameLAN(c.RemoteMultiaddr()) {
				route = "lan"
			}
			s, e := n.Host.NewStream(network.WithForceDirectDial(ctx, "p2pm-route"), p, proto)
			if e == nil {
				n.setRoute(p, "connected", route)
				return s, route, nil
			}
		}
	}
	n.routeMu.Lock()
	last := n.routeChecked[p]
	local := append([]ma.Multiaddr{}, n.lanPeers[p]...)
	n.routeMu.Unlock()
	if time.Since(last) < 30*time.Second {
		if s, route, e := n.baseStream(ctx, p, proto); e == nil {
			n.setRoute(p, "connected", route)
			return s, route, nil
		}
	}
	n.setRoute(p, "trying_lan", "")
	if n.knownContact(p) {
		if s, e := n.tryDirect(ctx, p, local, proto, "lan"); e == nil {
			return s, "lan", nil
		}
	}
	// Discovery/control may use relay; the file payload still tries LAN first.
	n.setRoute(p, "negotiating", "")
	var public []ma.Multiaddr
	control, _, e := n.baseStream(ctx, p, routeProtocol)
	if e == nil {
		control.SetDeadline(deadline(ctx, 4*time.Second))
		if wire.Write(control, n.localOffer()) == nil {
			var o routeOffer
			if wire.Read(control, &o) == nil {
				public = n.acceptOffer(p, o)
			}
		}
		control.Close()
	}
	n.routeMu.Lock()
	local = append([]ma.Multiaddr{}, n.lanPeers[p]...)
	n.routeMu.Unlock()
	n.setRoute(p, "trying_lan", "")
	if n.knownContact(p) {
		if s, e := n.tryDirect(ctx, p, local, proto, "lan"); e == nil {
			return s, "lan", nil
		}
	}
	n.setRoute(p, "trying_direct", "")
	if found, e := n.Find(ctx, p, false); e == nil {
		for _, raw := range found.Addresses {
			a, e := ma.NewMultiaddr(raw)
			if e == nil && netstack.PublicIP(ipOf(a)) && len(a.Protocols()) == 2 {
				public = append(public, a)
			}
		}
	}
	if s, e := n.tryDirect(ctx, p, public, proto, "direct"); e == nil {
		return s, "direct", nil
	}
	// DCUtR runs in libp2p after the relay connection; give it a short window.
	timer := time.NewTimer(2 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	defer tick.Stop()
waiting:
	for {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-timer.C:
			break waiting
		case <-tick.C:
			for _, c := range n.Host.Network().ConnsToPeer(p) {
				if !c.Stat().Limited {
					if s, e := n.Host.NewStream(network.WithForceDirectDial(ctx, "p2pm-holepunch"), p, proto); e == nil {
						route := "direct"
						if sameLAN(c.RemoteMultiaddr()) {
							route = "lan"
						}
						n.setRoute(p, "connected", route)
						return s, route, nil
					}
				}
			}
		}
	}
	n.setRoute(p, "trying_relay", "")
	n.routeMu.Lock()
	n.routeChecked[p] = time.Now()
	n.routeMu.Unlock()
	s, route, e := n.baseStream(ctx, p, proto)
	if e != nil {
		n.setRoute(p, "unreachable", "")
		return nil, "", e
	}
	n.setRoute(p, "connected", route)
	return s, route, nil
}
