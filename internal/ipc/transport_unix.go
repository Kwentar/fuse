//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
)

func address(dir string) (string, error) { return filepath.Join(dir, "control.sock"), nil }
func Listen(dir string) (net.Listener, error) {
	p, _ := address(dir)
	if e := os.Remove(p); e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	l, e := net.Listen("unix", p)
	if e != nil {
		return nil, e
	}
	if e = os.Chmod(p, 0600); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
func Dial(ctx context.Context, dir string) (net.Conn, error) {
	p, _ := address(dir)
	return (&net.Dialer{}).DialContext(ctx, "unix", p)
}
