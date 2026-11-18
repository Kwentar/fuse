//go:build !windows

package securefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFileRejectsSymlinksAndHardlinks(t *testing.T) {
	dir := t.TempDir()
	if e := Protect(dir); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(dir, "original")
	if e := os.WriteFile(target, []byte("keep"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, link := range []struct {
		name string
		make func(string, string) error
	}{{"symbolic", os.Symlink}, {"hard", os.Link}} {
		path := filepath.Join(dir, link.name)
		if e := link.make(target, path); e != nil {
			t.Fatal(e)
		}
		if f, e := OpenPrivateFile(path, os.O_RDWR); e == nil {
			f.Close()
			t.Fatalf("opened %s link", link.name)
		}
		os.Remove(path)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "keep" {
		t.Fatal("target changed")
	}
}
func TestExclusiveProfileLockAndPermissions(t *testing.T) {
	dir := t.TempDir()
	if e := Protect(dir); e != nil {
		t.Fatal(e)
	}
	first, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Acquire(dir); e == nil {
		second.Close()
		t.Fatal("second lock acquired")
	}
	first.Close()
	next, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	next.Close()
	if e := Check(filepath.Join(dir, "node.lock")); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(filepath.Join(dir, "node.lock"), 0644); e != nil {
		t.Fatal(e)
	}
	if lock, e := Acquire(dir); e == nil {
		lock.Close()
		t.Fatal("unsafe lock accepted")
	}
}
