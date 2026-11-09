package identity

import (
	"fuse/internal/securefs"
	"github.com/libp2p/go-libp2p/core/peer"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityStableAndCorruptionFatal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "identity")
	if e := securefs.EnsureDir(dir); e != nil {
		t.Fatal(e)
	}
	id, e := Init(dir)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 3; i++ {
		same, e := Init(dir)
		if e != nil || same != id {
			t.Fatal("identity changed", e)
		}
		key, e := Load(dir)
		if e != nil {
			t.Fatal(e)
		}
		loaded, _ := peer.IDFromPrivateKey(key)
		if loaded != id {
			t.Fatal("identity changed on load")
		}
	}
	path := filepath.Join(dir, "identity.key")
	if e = securefs.AtomicWrite(path, []byte("damaged")); e != nil {
		t.Fatal(e)
	}
	if _, e = Init(dir); e == nil {
		t.Fatal("damaged key replaced")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "damaged" {
		t.Fatal("key modified")
	}
}
