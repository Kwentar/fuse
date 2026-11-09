package ipc

import (
	"context"
	"fuse/internal/securefs"
	"github.com/Microsoft/go-winio"
	"net"
)

func address(dir string) (string, error) { hash, e := name(dir); return `\\.\pipe\p2pm-` + hash, e }
func Listen(dir string) (net.Listener, error) {
	p, e := address(dir)
	if e != nil {
		return nil, e
	}
	sd, e := securefs.Descriptor()
	if e != nil {
		return nil, e
	}
	return winio.ListenPipe(p, &winio.PipeConfig{SecurityDescriptor: sd, InputBufferSize: 65536, OutputBufferSize: 65536})
}
func Dial(ctx context.Context, dir string) (net.Conn, error) {
	p, e := address(dir)
	if e != nil {
		return nil, e
	}
	return winio.DialPipeContext(ctx, p)
}
