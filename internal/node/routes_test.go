package node

import (
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"net"
	"testing"
)

func TestRouteListenersUseProvidedAndroidInterfaces(t *testing.T) {
	listeners := []ma.Multiaddr{ma.StringCast("/ip4/0.0.0.0/tcp/50123")}
	addresses := []net.Addr{
		&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
		&net.IPNet{IP: net.ParseIP("192.168.1.50"), Mask: net.CIDRMask(24, 32)},
	}
	got := resolveRouteListeners(listeners, addresses)
	if len(got) != 1 || got[0].String() != "/ip4/192.168.1.50/tcp/50123" {
		t.Fatal(got)
	}
	if got = resolveRouteListeners(listeners, nil); len(got) != 0 {
		t.Fatal("unexpected host-interface fallback", got)
	}
}

func TestSmartRoutingPrefersLAN(t *testing.T) {
	ctx, a, b, _, _ := testPair(t, true)
	defer b.Close()
	if e := a.AddContact(b.Host.ID(), "B"); e != nil {
		t.Fatal(e)
	}
	if e := b.AddContact(a.Host.ID(), "A"); e != nil {
		t.Fatal(e)
	}
	addresses, _ := b.Host.Network().InterfaceListenAddresses()
	hasLAN := false
	for _, addr := range addresses {
		if sameLAN(addr) {
			hasLAN = true
		}
	}
	if !hasLAN {
		t.Skip("requires a private LAN interface")
	}
	a.HandlePeerFound(peer.AddrInfo{ID: b.Host.ID(), Addrs: addresses})
	stream, route, e := a.smartStream(ctx, b.Host.ID(), fileProtocol)
	if e != nil {
		t.Fatal(e)
	}
	defer stream.Close()
	if route != "lan" {
		t.Fatalf("expected LAN, got %s", route)
	}
	status := a.Status().Routes[b.Host.ID().String()]
	if status.Route != "lan" || status.Stage != "connected" {
		t.Fatal(status)
	}
	a.Host.Network().ClosePeer(b.Host.ID())
	if a.Status().Routes[b.Host.ID().String()].Stage != "disconnected" {
		t.Fatal("stale connected status")
	}
}
