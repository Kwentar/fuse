package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fuse/internal/securefs"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitFile(t *testing.T, n *Node, id string, timeout time.Duration) FileRecord {
	t.Helper()
	end := time.Now().Add(timeout)
	var last FileRecord
	for time.Now().Before(end) {
		rows, e := n.Files("")
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range rows {
			if r.ID == id && r.Direction == "out" {
				last = r
				if r.Status == "delivered" {
					return r
				}
				if r.Status == "paused" {
					t.Fatalf("transfer paused: %+v", r)
				}
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("file timeout: %+v", last)
	return last
}
func TestChunkedFileTransfer(t *testing.T) {
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	if e := a.AddContact(b.Host.ID(), "B"); e != nil {
		t.Fatal(e)
	}
	if e := b.AddContact(a.Host.ID(), "A"); e != nil {
		t.Fatal(e)
	}
	data := make([]byte, 20<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	path := filepath.Join(t.TempDir(), "файл с пробелом.dat")
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	r, e := a.SendFile(ctx, b.Host.ID(), path)
	if e != nil {
		t.Fatal(e)
	}
	r = waitFile(t, a, r.ID, 60*time.Second)
	if r.Progress != int64(len(data)) {
		t.Fatal(r.Progress)
	}
	rows, e := b.Files(a.Host.ID().String())
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	received, e := os.ReadFile(rows[0].Path)
	if e != nil {
		t.Fatal(e)
	}
	if sha256.Sum256(received) != sha256.Sum256(data) || rows[0].Status != "stored" {
		t.Fatal("file content/status mismatch")
	}
}
func TestFileResumeAfterReceiverRestart(t *testing.T) {
	ctx, a, b, dir, c := directPair(t)
	closed := false
	defer func() {
		if !closed {
			b.Close()
		}
	}()
	a.AddContact(b.Host.ID(), "B")
	b.AddContact(a.Host.ID(), "A")
	data := []byte("first chunk then resume")
	h := sha256.Sum256(data)
	q := fileRequest{Version: 2, ID: uuid.NewString(), Name: "resume.bin", Size: int64(len(data)), SHA: hex.EncodeToString(h[:]), Action: "chunk", Length: 5}
	ch := sha256.Sum256(data[:5])
	q.ChunkSHA = hex.EncodeToString(ch[:])
	r, _, e := a.fileExchange(ctx, b.Host.ID(), q, data[:5])
	if e != nil || r.Status != "accepted" || r.Offset != 5 {
		t.Fatal(r, e)
	}
	bid := b.Host.ID()
	b.Close()
	closed = true
	b, e = Open(ctx, dir, c)
	if e != nil {
		t.Fatal(e)
	}
	closed = false
	for i := 0; i < 30; i++ {
		a.Find(ctx, bid, true)
		time.Sleep(100 * time.Millisecond)
	}
	q.Action = "status"
	q.Length = 0
	r, _, e = a.fileExchange(ctx, bid, q, nil)
	if e != nil || r.Offset != 5 {
		t.Fatal(r, e)
	}
	q.Action = "chunk"
	q.Offset = 5
	q.Length = int64(len(data) - 5)
	ch = sha256.Sum256(data[5:])
	q.ChunkSHA = hex.EncodeToString(ch[:])
	r, _, e = a.fileExchange(ctx, bid, q, data[5:])
	if e != nil || r.Status != "accepted" {
		t.Fatal(r, e)
	}
	q.Action = "finish"
	q.Offset = int64(len(data))
	q.Length = 0
	r, _, e = a.fileExchange(ctx, bid, q, nil)
	if e != nil || r.Status != "stored" {
		t.Fatal(r, e)
	}
}
func TestFileMetadataBeyondTenGiB(t *testing.T) {
	h := sha256.Sum256(nil)
	q := fileRequest{Version: 2, ID: uuid.NewString(), Name: "large.bin", Size: 10 << 30, SHA: hex.EncodeToString(h[:]), Action: "chunk", Offset: 9 << 30, Length: FileChunk}
	if !validRequest(q) {
		t.Fatal("10 GiB metadata rejected")
	}
	q.Offset = q.Size - 1
	q.Length = 2
	if validRequest(q) {
		t.Fatal("chunk beyond EOF accepted")
	}
	q.Name = "../bad"
	if validRequest(q) {
		t.Fatal("path traversal accepted")
	}
}
func TestTenGiBEndToEnd(t *testing.T) {
	if os.Getenv("P2PM_LARGE_TEST") != "1" {
		t.Skip("set P2PM_LARGE_TEST=1 for actual 10 GiB transfer")
	}
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	_ = ctx
	a.AddContact(b.Host.ID(), "B")
	b.AddContact(a.Host.ID(), "A")
	path := filepath.Join(t.TempDir(), "ten-gib.bin")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(10 << 30); e != nil {
		t.Fatal(e)
	}
	f.Close()
	r, e := a.SendFile(context.Background(), b.Host.ID(), path)
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	r = waitFile(t, a, r.ID, 20*time.Minute)
	if r.Progress != 10<<30 {
		t.Fatal(r.Progress)
	}
	rows, e := b.Files(a.Host.ID().String())
	if e != nil || len(rows) != 1 || rows[0].SHA != r.SHA {
		t.Fatal(rows, e)
	}
	if e = securefs.Check(rows[0].Path); e != nil {
		t.Fatal(e)
	}
	t.Logf("actual 10 GiB transfer completed in %s, SHA-256 matched", time.Since(start))
}
