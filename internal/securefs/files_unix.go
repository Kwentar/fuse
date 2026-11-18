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
	s, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if s.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlinks are not allowed")
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
	f, e := OpenPrivateFile(filepath.Join(dir, "node.lock"), os.O_CREATE|os.O_RDWR)
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

// The parent directory is private; O_NOFOLLOW also rejects a substituted leaf.
func OpenPrivateFile(path string, flags int) (*os.File, error) {
	if e := Check(filepath.Dir(path)); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(path, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	s, e := f.Stat()
	if e == nil {
		st, ok := s.Sys().(*syscall.Stat_t)
		if !s.Mode().IsRegular() || s.Mode().Perm()&0077 != 0 || !ok || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
			e = errors.New("PERMISSION_ERROR: unsafe private file")
		}
	}
	if e != nil {
		f.Close()
		return nil, e
	}
	return f, nil
}
