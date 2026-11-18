//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"fuse/internal/securefs"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

func address(dir string) (string, error) {
	p := filepath.Join(dir, "control.sock")
	limit := 107
	if runtime.GOOS == "darwin" {
		limit = 103
	}
	if len(p) > limit {
		return "", fmt.Errorf("IPC_PATH_TOO_LONG: socket path is %d bytes, maximum %d; choose a shorter profile path", len(p), limit)
	}
	return p, nil
}
func Listen(dir string) (net.Listener, error) {
	p, e := address(dir)
	if e != nil {
		return nil, e
	}
	if e = securefs.Check(dir); e != nil {
		return nil, e
	}
	if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("IPC_UNSAFE_PATH: refusing to remove a non-socket")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
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
	p, e := address(dir)
	if e != nil {
		return nil, e
	}
	if e = securefs.Check(dir); e != nil {
		return nil, e
	}
	if e = securefs.Check(p); e != nil {
		return nil, e
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", p)
}
