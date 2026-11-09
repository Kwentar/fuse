package integration

import (
	"context"
	"crypto/rand"
	"io"
	"testing"
	"time"

	netstack "fuse/internal/network"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

func key(t *testing.T) crypto.PrivKey {
	t.Helper()
	k, _, e := crypto.GenerateEd25519Key(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return k
}

func TestLimitedCircuitNoise(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, e := netstack.NewHost(key(t), []string{"/ip4/127.0.0.1/tcp/0"}, nil, true, true)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	resources := relay.DefaultResources()
	resources.Limit = &relay.RelayLimit{Duration: time.Hour, Data: 16 << 20}
	resources.MaxReservations = 64
	resources.MaxCircuits = 4
	resources.MaxReservationsPerIP = 32
	resources.MaxReservationsPerASN = 64
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
	for _, addr := range append(a.Network().ListenAddresses(), b.Network().ListenAddresses()...) {
		if _, err := addr.ValueForProtocol(ma.P_TCP); err == nil {
			t.Fatal("unexpected direct TCP listener")
		}
	}
	if _, e = client.Reserve(ctx, b, peer.AddrInfo{ID: r.ID(), Addrs: r.Addrs()}); e != nil {
		t.Fatal(e)
	}
	b.SetStreamHandler("/p2p-messenger/chat/1.0.0", func(s network.Stream) {
		defer s.Close()
		if s.Conn().RemotePeer() != a.ID() {
			s.Reset()
			return
		}
		io.Copy(s, s)
	})
	address := r.Addrs()[0].Encapsulate(ma.StringCast("/p2p/" + r.ID().String() + "/p2p-circuit"))
	if e = a.Connect(network.WithAllowLimitedConn(ctx, "p2pm-chat"), peer.AddrInfo{ID: b.ID(), Addrs: []ma.Multiaddr{address}}); e != nil {
		t.Fatal(e)
	}
	s, e := a.NewStream(network.WithAllowLimitedConn(ctx, "p2pm-chat"), b.ID(), "/p2p-messenger/chat/1.0.0")
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(5 * time.Second))
	if !s.Conn().Stat().Limited {
		t.Fatal("circuit must be limited")
	}
	remote, e := peer.IDFromPublicKey(s.Conn().RemotePublicKey())
	if e != nil || remote != b.ID() || s.Conn().RemotePeer() != b.ID() {
		t.Fatal("authenticated remote key differs from expected endpoint")
	}
	// v0.41.1 circuit transport returns an empty Security field in ConnState.
	// NewHost configures Noise as its only secure transport; verify the endpoint
	// public key and reject a deliberately substituted destination below.
	if _, e = s.Write([]byte("relay-check")); e != nil {
		t.Fatal(e)
	}
	response := make([]byte, 11)
	if _, e = io.ReadFull(s, response); e != nil || string(response) != "relay-check" {
		t.Fatalf("echo failed: %v", e)
	}
	wrong, _ := peer.IDFromPrivateKey(key(t))
	badctx, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	if e = a.Connect(network.WithAllowLimitedConn(badctx, "negative-auth"), peer.AddrInfo{ID: wrong, Addrs: []ma.Multiaddr{address}}); e == nil {
		t.Fatal("substituted endpoint was accepted")
	}
	t.Log("limited circuit: TCP + Noise + Yamux; authenticated endpoint; no client TCP listener")
}
