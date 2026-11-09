package node

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fuse/internal/appinit"
	"fuse/internal/config"
	"fuse/internal/messaging"
	"fuse/internal/server"
	"fuse/internal/storage"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/network"
)

func directPair(t *testing.T) (context.Context, *Node, *Node, string, config.Client) {
	return testPair(t, false)
}
func testPair(t *testing.T, smart bool) (context.Context, *Node, *Node, string, config.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	t.Cleanup(cancel)
	sc := config.DefaultServer()
	sc.DataDir = filepath.Join(t.TempDir(), "server")
	sc.Listen = []string{"/ip4/127.0.0.1/tcp/0"}
	sc.AllowPrivate = true
	s, e := server.Open(ctx, sc)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Close)
	c := config.DefaultClient()
	c.AllowPrivate = true
	c.PublishInterval = "2s"
	c.Direct = []string{"/ip4/127.0.0.1/tcp/0"}
	c.SmartRouting = smart
	if smart {
		c.Direct = []string{"/ip4/0.0.0.0/tcp/0"}
	}
	c.Bootstrap = []string{s.Host.Addrs()[0].String() + "/p2p/" + s.Host.ID().String()}
	c.Relays = append([]string{}, c.Bootstrap...)
	dirA, dirB := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	if _, e = appinit.Init(dirA, c); e != nil {
		t.Fatal(e)
	}
	if _, e = appinit.Init(dirB, c); e != nil {
		t.Fatal(e)
	}
	a, e := Open(ctx, dirA, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(a.Close)
	b, e := Open(ctx, dirB, c)
	if e != nil {
		t.Fatal(e)
	}
	end := time.Now().Add(20 * time.Second)
	for time.Now().Before(end) {
		f, e := a.Find(ctx, b.Host.ID(), true)
		if e == nil && len(f.Addresses) > 0 {
			return ctx, a, b, dirB, c
		}
		time.Sleep(150 * time.Millisecond)
	}
	b.Close()
	t.Fatal("contact not published")
	return nil, nil, nil, "", c
}
func TestLostACKRestartRetryAndDirect(t *testing.T) {
	ctx, a, b, dirB, c := directPair(t)
	bid := b.Host.ID()
	closed := false
	defer func() {
		if !closed {
			b.Close()
		}
	}()
	b.Host.SetStreamHandler(messaging.Protocol, func(s network.Stream) {
		defer s.Reset()
		s.SetDeadline(time.Now().Add(5 * time.Second))
		var m messaging.Message
		if wire.Read(s, &m) != nil {
			return
		}
		b.Journal.Receive(s.Conn().RemotePeer(), m)
	})
	m, _ := messaging.New("durably saved, ACK lost")
	r, e := a.Send(ctx, bid, m, false)
	if e != nil || r.Status != "delivery_unknown" {
		t.Fatalf("%+v %v", r, e)
	}
	b.Close()
	closed = true
	var err error
	b, err = Open(ctx, dirB, c)
	if err != nil {
		t.Fatal(err)
	}
	closed = false
	// Old direct address changed; wait until the new signed record is visible.
	end := time.Now().Add(20 * time.Second)
	for time.Now().Before(end) {
		f, e := a.Find(ctx, bid, true)
		if e == nil {
			matched := false
			for _, addr := range b.contactAddresses() {
				for _, found := range f.Addresses {
					if found == addr.String() {
						matched = true
					}
				}
			}
			if matched {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	r, e = a.Retry(ctx, m.ID)
	if e != nil || r.Status != "delivered" || r.ACK != "duplicate" || r.Route != "direct" {
		t.Fatalf("%+v %v", r, e)
	}
	count := 0
	b.Journal.History(a.Host.ID().String(), 50, func(event storage.Event) error {
		if event.ID == m.ID {
			count++
		}
		return nil
	})
	if count != 1 {
		t.Fatal("duplicate history", count)
	}
	r, e = a.Retry(ctx, m.ID)
	if e != nil || r.Status != "delivered" || r.ACK != "" {
		t.Fatal("confirmed retry sent again", r, e)
	}
	s, _, e := a.openStream(ctx, bid)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	s.SetDeadline(time.Now().Add(5 * time.Second))
	m.Text = "conflicting contents"
	if e = wire.Write(s, m); e != nil {
		t.Fatal(e)
	}
	var reply messaging.Reply
	if e = wire.Read(s, &reply); e != nil || reply.Code != "MESSAGE_ID_CONFLICT" {
		t.Fatal(reply, e)
	}
	large, _ := messaging.New(strings.Repeat("\x00", 65536))
	r, e = a.Send(ctx, bid, large, false)
	if e != nil || r.Status != "delivered" {
		t.Fatal(r, e)
	}
}
func TestStreamBudgetRecovers(t *testing.T) {
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	var held []network.Stream
	defer func() {
		for _, s := range held {
			s.Reset()
		}
	}()
	for i := 0; i < 2; i++ {
		s, _, e := a.openStream(ctx, b.Host.ID())
		if e != nil {
			t.Fatal(e)
		}
		held = append(held, s)
		// Force lazy protocol negotiation; leave the length prefix incomplete.
		if _, e = s.Write([]byte{0, 0}); e != nil {
			t.Fatal(e)
		}
	}
	time.Sleep(100 * time.Millisecond)
	s, _, e := a.openStream(ctx, b.Host.ID())
	if e != nil {
		t.Fatal(e)
	}
	s.SetDeadline(time.Now().Add(time.Second))
	m, _ := messaging.New("over budget")
	e = wire.Write(s, m)
	var reply messaging.Reply
	if e == nil {
		e = wire.Read(s, &reply)
	}
	s.Close()
	if e == nil {
		t.Fatal("third concurrent chat stream accepted")
	}
	for _, s := range held {
		s.Reset()
	}
	held = nil
	time.Sleep(150 * time.Millisecond)
	m, _ = messaging.New("after release")
	r, e := a.Send(ctx, b.Host.ID(), m, false)
	if e != nil || r.Status != "delivered" {
		t.Fatal(r, e)
	}
}

func TestBadACKIsUnknownAndStorageErrorIsRejected(t *testing.T) {
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	for _, test := range []struct{ name, typ, status, code, id, expected string }{{"bad-ack", "ack", "stored", "", "wrong-id", "delivery_unknown"}, {"disk-full", "error", "", "STORAGE_ERROR", "", "rejected"}} {
		t.Run(test.name, func(t *testing.T) {
			b.Host.SetStreamHandler(messaging.Protocol, func(s network.Stream) {
				defer s.Close()
				s.SetDeadline(time.Now().Add(5 * time.Second))
				var m messaging.Message
				if wire.Read(s, &m) != nil {
					return
				}
				id := m.ID
				if test.id != "" {
					id = test.id
				}
				wire.Write(s, messaging.Reply{Version: 1, Type: test.typ, ID: id, Status: test.status, Code: test.code})
			})
			m, _ := messaging.New(test.name)
			r, e := a.Send(ctx, b.Host.ID(), m, false)
			if e != nil || r.Status != test.expected {
				t.Fatal(r, e)
			}
		})
	}
}
