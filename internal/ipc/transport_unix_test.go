//go:build !windows

package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPathLimit(t *testing.T) {
	if _, e := address("/" + strings.Repeat("a", 108)); e == nil {
		t.Fatal("overlong socket path accepted")
	}
}
func TestListenDoesNotDeleteRegularFile(t *testing.T) {
	dir, e := os.MkdirTemp("", "p2pm-ipc-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "control.sock")
	if e = os.WriteFile(path, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	if l, e := Listen(dir); e == nil {
		l.Close()
		t.Fatal("replaced non-socket")
	}
	b, e := os.ReadFile(path)
	if e != nil || string(b) != "keep" {
		t.Fatal("file changed", e)
	}
}
