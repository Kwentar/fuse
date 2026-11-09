package integration

import (
	"bytes"
	"context"
	netstack "fuse/internal/network"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
	"io"
	"testing"
	"time"
)

func TestRelayByteLimitAndNewCircuit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = network.WithAllowLimitedConn(ctx, "p2pm-chat")
	r, e := netstack.NewHost(key(t), []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, true)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	resources := relay.DefaultResources()
	resources.Limit = &relay.RelayLimit{Duration: time.Minute, Data: 32 << 10}
	svc, e := relay.New(r, relay.WithResources(resources))
	if e != nil {
		t.Fatal(e)
	}
	defer svc.Close()
	a, e := netstack.NewHost(key(t), nil, nil, false, true)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := netstack.NewHost(key(t), nil, nil, false, true)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	if _, e = client.Reserve(ctx, b, peer.AddrInfo{ID: r.ID(), Addrs: r.Addrs()}); e != nil {
		t.Fatal(e)
	}
	b.SetStreamHandler("/p2p-messenger/chat/1.0.0", func(s network.Stream) { defer s.Close(); s.SetDeadline(time.Now().Add(5 * time.Second)); io.Copy(s, s) })
	address := r.Addrs()[0].Encapsulate(ma.StringCast("/p2p/" + r.ID().String() + "/p2p-circuit"))
	if e = a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: []ma.Multiaddr{address}}); e != nil {
		t.Fatal(e)
	}
	s, e := a.NewStream(ctx, b.ID(), "/p2p-messenger/chat/1.0.0")
	if e != nil {
		t.Fatal(e)
	}
	s.SetDeadline(time.Now().Add(5 * time.Second))
	payload := bytes.Repeat([]byte{'x'}, 128<<10)
	written := make(chan error, 1)
	go func() { _, e := s.Write(payload); written <- e }()
	got := make([]byte, len(payload))
	_, readErr := io.ReadFull(s, got)
	writeErr := <-written
	s.Close()
	if readErr == nil && writeErr == nil {
		t.Fatal("relay byte limit not enforced")
	}
	// Permit teardown to propagate, then NewStream must dial a fresh circuit.
	time.Sleep(200 * time.Millisecond)
	// Like the application, restore the signed contact address after Identify
	// removes the no-listener endpoint's transient peerstore address.
	if e = a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: []ma.Multiaddr{address}}); e != nil {
		t.Fatal(e)
	}
	fresh, e := a.NewStream(ctx, b.ID(), "/p2p-messenger/chat/1.0.0")
	if e != nil {
		t.Fatal(e)
	}
	defer fresh.Close()
	fresh.SetDeadline(time.Now().Add(3 * time.Second))
	if _, e = fresh.Write([]byte("new-circuit")); e != nil {
		t.Fatal(e)
	}
	buf := make([]byte, 11)
	if _, e = io.ReadFull(fresh, buf); e != nil || string(buf) != "new-circuit" {
		t.Fatal("new circuit failed", e)
	}
}
