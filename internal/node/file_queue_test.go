package node

import (
	"context"
	"crypto/sha256"
	"fmt"
	"fuse/internal/securefs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestThousandFileQueue(t *testing.T) {
	if os.Getenv("P2PM_BATCH_TEST") != "1" {
		t.Skip("set P2PM_BATCH_TEST=1")
	}
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	a.AddContact(b.Host.ID(), "B")
	b.AddContact(a.Host.ID(), "A")
	dir := t.TempDir()
	data := []byte("small photo fixture\n")
	start := time.Now()
	for i := 0; i < 1010; i++ {
		path := filepath.Join(dir, fmt.Sprintf("photo-%04d.jpg", i))
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := a.SendFile(ctx, b.Host.ID(), path); e != nil {
			t.Fatal(i, e)
		}
	}
	until := time.Now().Add(3 * time.Minute)
	for time.Now().Before(until) {
		rows, e := a.Files("")
		if e != nil {
			t.Fatal(e)
		}
		done := 0
		for _, r := range rows {
			if r.Status == "paused" {
				t.Fatal(r.Error)
			}
			if r.Status == "delivered" {
				done++
			}
		}
		if done == 1010 {
			incoming, _ := b.Files("")
			if len(incoming) != 1010 {
				t.Fatal(len(incoming))
			}
			for _, r := range incoming {
				got, e := os.ReadFile(r.Path)
				if e != nil || sha256.Sum256(got) != sha256.Sum256(data) {
					t.Fatal(r.Name, e)
				}
			}
			t.Logf("1010 files transferred and verified in %s", time.Since(start))
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("queue timeout")
}
func TestAutoDownloadsNoOverwrite(t *testing.T) {
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	a.AddContact(b.Host.ID(), "B")
	b.AddContact(a.Host.ID(), "A")
	dir := t.TempDir()
	if e := b.SetDownloadDir(dir); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("existing"), 0600); e != nil {
		t.Fatal(e)
	}
	source := filepath.Join(t.TempDir(), "photo.jpg")
	os.WriteFile(source, []byte("incoming"), 0600)
	r, e := a.SendFile(ctx, b.Host.ID(), source)
	if e != nil {
		t.Fatal(e)
	}
	waitFile(t, a, r.ID, 20*time.Second)
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		rows, _ := b.Files("")
		if len(rows) > 0 && rows[0].ExportPath != "" {
			got, _ := os.ReadFile(rows[0].ExportPath)
			if string(got) != "incoming" {
				t.Fatal(string(got))
			}
			old, _ := os.ReadFile(filepath.Join(dir, "photo.jpg"))
			if string(old) != "existing" {
				t.Fatal("overwritten")
			}
			if rows[0].Path != rows[0].ExportPath {
				t.Fatal("duplicate private copy retained")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("auto export timeout")
}
func TestLegacyFileFallback(t *testing.T) {
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	b.Host.RemoveStreamHandler(fileSessionProtocol)
	a.AddContact(b.Host.ID(), "B")
	b.AddContact(a.Host.ID(), "A")
	path := filepath.Join(t.TempDir(), "legacy.bin")
	os.WriteFile(path, make([]byte, 9<<20), 0600)
	r, e := a.SendFile(ctx, b.Host.ID(), path)
	if e != nil {
		t.Fatal(e)
	}
	r = waitFile(t, a, r.ID, 30*time.Second)
	if r.Progress != 9<<20 {
		t.Fatal(r)
	}
}
func TestQueuePauseBeforeStart(t *testing.T) {
	n := &Node{dir: t.TempDir(), ctx: context.Background(), fileRecords: map[string]FileRecord{}}
	securefs.Protect(n.dir)
	securefs.EnsureDir(filepath.Join(n.dir, "files"))
	// A paused queued item must never be selected by a later stale scheduler snapshot.
	r := FileRecord{ID: "550e8400-e29b-41d4-a716-446655440000", Peer: "test", Direction: "out", Status: "queued"}
	if e := n.saveFileRecord(r); e != nil {
		t.Fatal(e)
	}
	if e := n.FileAction(r.ID, "pause"); e != nil {
		t.Fatal(e)
	}
	rows, _ := n.Files("")
	if rows[0].Status != "paused" {
		t.Fatal(rows)
	}
}

func TestQueuedFileRestartAndIdempotency(t *testing.T) {
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	a.AddContact(b.Host.ID(), "B")
	b.AddContact(a.Host.ID(), "A")
	source := filepath.Join(t.TempDir(), "retry.bin")
	os.WriteFile(source, []byte("durable queue"), 0600)
	id := "550e8400-e29b-41d4-a716-446655440001"
	r, e := a.QueueFile(b.Host.ID(), source, id)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.QueueFile(b.Host.ID(), source, id); e != nil {
		t.Fatal(e)
	}
	waitFile(t, a, r.ID, 20*time.Second)
	rows, _ := a.Files(b.Host.ID().String())
	if len(rows) != 1 {
		t.Fatal("retry created duplicate", len(rows))
	}
	_ = ctx
	// A restart restores interrupted work but preserves an explicit pause.
	r.Status = "sending"
	a.saveFileRecord(r)
	copyNode := &Node{dir: a.dir}
	if e = copyNode.loadFiles(); e != nil {
		t.Fatal(e)
	}
	loaded := copyNode.fileRecords[copyNode.fileRecordPath("out", r.Peer, r.ID)]
	if loaded.Status != "queued" {
		t.Fatal(loaded.Status)
	}
	r.Status = "paused"
	a.saveFileRecord(r)
	if e = copyNode.loadFiles(); e != nil {
		t.Fatal(e)
	}
	loaded = copyNode.fileRecords[copyNode.fileRecordPath("out", r.Peer, r.ID)]
	if loaded.Status != "paused" {
		t.Fatal(loaded.Status)
	}
}
