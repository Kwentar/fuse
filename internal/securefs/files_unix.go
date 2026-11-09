//go:build !windows

package securefs

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

func UserID() (string, error) { return strconv.Itoa(os.Getuid()), nil }
func Protect(path string) error {
	s, e := os.Stat(path)
	if e != nil {
		return e
	}
	m := os.FileMode(0600)
	if s.IsDir() {
		m = 0700
	}
	return os.Chmod(path, m)
}
func Check(path string) error {
	s, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if s.Mode()&os.ModeSymlink != 0 || s.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe file permissions")
	}
	if st, ok := s.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(os.Getuid()) {
		return errors.New("unexpected owner")
	}
	return nil
}
func replace(from, to string) error {
	if e := os.Rename(from, to); e != nil {
		return e
	}
	f, e := os.Open(filepath.Dir(to))
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

type Lock struct{ f *os.File }

func Acquire(dir string) (*Lock, error) {
	f, e := os.OpenFile(filepath.Join(dir, "node.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, errors.New("NODE_ALREADY_RUNNING: data directory is locked")
	}
	return &Lock{f}, nil
}
func (l *Lock) Close() error { return l.f.Close() }
