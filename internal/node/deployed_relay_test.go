package node

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fuse/internal/appinit"
	"fuse/internal/config"
	"fuse/internal/messaging"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Explicit opt-in, synthetic data and disposable identities only.
func TestDeployedRelay(t *testing.T) {
	path := os.Getenv("P2PM_RELAY_TEST_CONFIG")
	if path == "" {
		t.Skip("deployment test opt-in")
	}
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	var endpoints struct {
		Bootstrap []string `json:"bootstrap_nodes"`
		Relays    []string `json:"relay_nodes"`
	}
	if e = json.Unmarshal(data, &endpoints); e != nil {
		t.Fatal(e)
	}
	if len(endpoints.Relays) != 1 {
		t.Fatal("test requires exactly one relay")
	}
	c := config.DefaultClient()
	c.Bootstrap = endpoints.Bootstrap
	c.Relays = endpoints.Relays
	c.Direct = nil
	c.SmartRouting = false
	c.PublishInterval = "2s"
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	open := func() *Node {
		dir := filepath.Join(t.TempDir(), "profile")
		if _, e = appinit.Init(dir, c); e != nil {
			t.Fatal(e)
		}
		n, e := Open(ctx, dir, c)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(n.Close)
		return n
	}
	a, b := open(), open()
	a.AddContact(b.Host.ID(), "relay-test-B")
	b.AddContact(a.Host.ID(), "relay-test-A")
	found := false
	until := time.Now().Add(70 * time.Second)
	for time.Now().Before(until) {
		r, e := a.Find(ctx, b.Host.ID(), true)
		if e == nil && len(r.Addresses) > 0 {
			found = true
			break
		}
		time.Sleep(time.Second)
	}
	if !found {
		t.Fatal("discovery timeout")
	}
	m, _ := messaging.New("Synthetic relay deployment test")
	r, e := a.Send(ctx, b.Host.ID(), m, false)
	if e != nil || r.Status != "delivered" || r.Route != "relay" {
		t.Fatalf("message result: %+v %v", r, e)
	}
	payload := make([]byte, 32<<20)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	source := filepath.Join(t.TempDir(), "relay-test.bin")
	if e = os.WriteFile(source, payload, 0600); e != nil {
		t.Fatal(e)
	}
	f, e := a.SendFile(ctx, b.Host.ID(), source)
	if e != nil {
		t.Fatal(e)
	}
	f = waitFile(t, a, f.ID, 240*time.Second)
	if f.Route != "relay" {
		t.Fatal("not relay")
	}
	rows, e := b.Files(a.Host.ID().String())
	if e != nil || len(rows) != 1 {
		t.Fatal("missing inbound")
	}
	received, e := os.ReadFile(rows[0].Path)
	if e != nil || sha256.Sum256(received) != sha256.Sum256(payload) {
		t.Fatal("SHA mismatch")
	}
	t.Log("new relay: discovery, text, 32 MiB and SHA-256 passed; direct listeners disabled")
}
