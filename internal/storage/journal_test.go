package storage

import (
	"fuse/internal/messaging"
	"fuse/internal/securefs"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func setup(t *testing.T) (string, peer.ID) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	if e := securefs.EnsureDir(dir); e != nil {
		t.Fatal(e)
	}
	k, _, _ := crypto.GenerateEd25519Key(nil)
	p, _ := peer.IDFromPrivateKey(k)
	return dir, p
}
func TestDurableConcurrentDeduplication(t *testing.T) {
	dir, p := setup(t)
	j, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	m, _ := messaging.New(strings.Repeat("\x00", 64<<10))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := j.Receive(p, m); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	count := 0
	j.History("", 50, func(e Event) error { count++; return nil })
	if count != 1 {
		t.Fatal(count)
	}
	j.Close()
	j, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	s, e := j.Receive(p, m)
	if e != nil || s != "duplicate" {
		t.Fatal(s, e)
	}
	m.Text = "different"
	if _, e = j.Receive(p, m); e == nil || e.Error() != "MESSAGE_ID_CONFLICT" {
		t.Fatal("conflict accepted", e)
	}
}
func TestRecoveryAndStorageFailure(t *testing.T) {
	dir, p := setup(t)
	j, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	m, _ := messaging.New("pending")
	if e = j.Pending(p, m); e != nil {
		t.Fatal(e)
	}
	j.Close()
	f, _ := os.OpenFile(filepath.Join(dir, "messages.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"partial":`)
	f.Close()
	j, e = Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	saved, e := j.Outgoing(m.ID)
	if e != nil || saved.Status != "delivery_unknown" {
		t.Fatal(saved.Status, e)
	}
	tails, _ := filepath.Glob(filepath.Join(dir, "messages.jsonl.tail-*"))
	if len(tails) != 1 {
		t.Fatal("tail not preserved")
	}
	j.f.Close()
	incoming, _ := messaging.New("storage failure")
	if s, e := j.Receive(p, incoming); e == nil || s == "stored" {
		t.Fatal("success on failed storage")
	}
	j.Close()
}
func TestInteriorCorruptionIsFatal(t *testing.T) {
	dir, _ := setup(t)
	if e := securefs.AtomicWrite(filepath.Join(dir, "messages.jsonl"), []byte("bad json\n{}\n")); e != nil {
		t.Fatal(e)
	}
	if j, e := Open(dir); e == nil {
		j.Close()
		t.Fatal("corrupt journal accepted")
	}
}
