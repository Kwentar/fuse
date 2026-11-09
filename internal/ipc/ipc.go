package ipc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fuse/internal/securefs"
	"fuse/internal/wire"
	"github.com/google/uuid"
)

type Request struct {
	Version  int             `json:"version"`
	ID       string          `json:"request_id"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
	Deadline string          `json:"deadline"`
}
type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"request_id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
	Done    bool            `json:"done,omitempty"`
}

func name(dir string) (string, error) {
	p, e := filepath.Abs(dir)
	if e != nil {
		return "", e
	}
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	user, e := securefs.UserID()
	if e != nil {
		return "", e
	}
	h := sha256.Sum256([]byte(user + "\x00" + filepath.Clean(p)))
	return hex.EncodeToString(h[:16]), nil
}

type Handler func(context.Context, Request, func(any) error) error

func Serve(ctx context.Context, listener net.Listener, handler Handler) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	stop := context.AfterFunc(ctx, func() { listener.Close() })
	defer stop()
	sem := make(chan struct{}, 32)
	for {
		conn, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case sem <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			var req Request
			if e := wire.Read(conn, &req); e != nil {
				return
			}
			if req.Version != 1 || req.ID == "" || len(req.ID) > 64 {
				return
			}
			end, e := time.Parse(time.RFC3339Nano, req.Deadline)
			if e != nil {
				return
			}
			if max := time.Now().Add(30 * time.Second); end.After(max) {
				end = max
			}
			conn.SetDeadline(end)
			requestCtx, cancel := context.WithDeadline(ctx, end)
			defer cancel()
			emit := func(v any) error {
				b, e := json.Marshal(v)
				if e != nil {
					return e
				}
				return wire.Write(conn, Response{Version: 1, ID: req.ID, Result: b})
			}
			err := handler(requestCtx, req, emit)
			r := Response{Version: 1, ID: req.ID, Done: true}
			if err != nil {
				r.Error = err.Error()
			}
			wire.Write(conn, r)
		}()
	}
}
func Call(ctx context.Context, dir, method string, params any, emit func(json.RawMessage) error) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connect, stop := context.WithTimeout(ctx, 2*time.Second)
	conn, e := Dial(connect, dir)
	stop()
	if e != nil {
		return errors.New("NODE_NOT_RUNNING")
	}
	defer conn.Close()
	end, _ := ctx.Deadline()
	conn.SetDeadline(end)
	closeOnCancel := context.AfterFunc(ctx, func() { conn.Close() })
	defer closeOnCancel()
	b, e := json.Marshal(params)
	if e != nil {
		return e
	}
	id := uuid.NewString()
	req := Request{1, id, method, b, end.Format(time.RFC3339Nano)}
	if e = wire.Write(conn, req); e != nil {
		return errors.New("IPC_ERROR")
	}
	for {
		var response Response
		if e = wire.Read(conn, &response); e != nil {
			return errors.New("IPC_RESULT_UNKNOWN: check history before explicit retry")
		}
		if response.Version != 1 || response.ID != id {
			return errors.New("IPC_PROTOCOL_ERROR")
		}
		if response.Error != "" {
			return errors.New(response.Error)
		}
		if len(response.Result) > 0 {
			if e = emit(response.Result); e != nil {
				return e
			}
		}
		if response.Done {
			return nil
		}
	}
}
