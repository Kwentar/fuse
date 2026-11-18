package node

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fuse/internal/securefs"
	"github.com/google/uuid"
)

func TestFileBoundariesAndDiskHashes(t *testing.T) {
	ctx, a, b, _, _ := directPair(t)
	defer b.Close()
	if e := a.AddContact(b.Host.ID(), "Б"); e != nil {
		t.Fatal(e)
	}
	if e := b.AddContact(a.Host.ID(), "А"); e != nil {
		t.Fatal(e)
	}
	for _, size := range []int64{0, 1, FileChunk, FileChunk + 1} {
		data := make([]byte, int(size))
		for i := range data {
			data[i] = byte(i % 251)
		}
		path := filepath.Join(t.TempDir(), "Тест 🐈 с пробелом.bin")
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		sent, e := a.SendFile(ctx, b.Host.ID(), path)
		if e != nil {
			t.Fatal(e)
		}
		sent = waitFile(t, a, sent.ID, time.Minute)
		rows, e := b.Files(a.Host.ID().String())
		if e != nil {
			t.Fatal(e)
		}
		found := false
		for _, r := range rows {
			if r.ID == sent.ID {
				f, e := os.Open(r.Path)
				if e != nil {
					t.Fatal(e)
				}
				h := sha256.New()
				n, e := io.Copy(h, f)
				f.Close()
				expected := sha256.Sum256(data)
				if e != nil || n != size || hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(expected[:]) || r.Status != "stored" {
					t.Fatal("on-disk file mismatch", size, e)
				}
				t.Logf("bytes=%d route=%s sha256=%x", size, sent.Route, h.Sum(nil))
				found = true
			}
		}
		if !found {
			t.Fatal("receiver record missing")
		}
	}
}
func TestRecoveryKeepsAllUnfinishedTasks(t *testing.T) {
	dir := t.TempDir()
	if e := securefs.EnsureDir(filepath.Join(dir, "files")); e != nil {
		t.Fatal(e)
	}
	n := &Node{dir: dir}
	for i := 0; i < 105; i++ {
		r := FileRecord{Version: 2, ID: uuid.NewString(), Peer: "test", Direction: "out", Status: "sending", Size: 10 << 30, Progress: 9 << 30}
		if e := n.saveFileRecord(r); e != nil {
			t.Fatal(e)
		}
	}
	if e := n.loadFiles(); e != nil {
		t.Fatal(e)
	}
	rows, e := n.Files("")
	if e != nil || len(rows) != 105 {
		t.Fatal(len(rows), e)
	}
	for _, r := range rows {
		if r.Status != "queued" || r.Progress != 9<<30 {
			t.Fatal("recovery lost offset", r)
		}
	}
}
