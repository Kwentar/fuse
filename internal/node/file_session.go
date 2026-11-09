package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"io"
	"os"
	"time"
)

// 2.1 keeps the Yamux stream (and its grown receive window) across chunks.
// 2.0 peers retain the exact original wire format and one request per stream.
type fileSession struct {
	n      *Node
	p      peer.ID
	s      network.Stream
	route  string
	legacy bool
	opened time.Time
}

func (s *fileSession) close() {
	if s.s != nil {
		s.s.Close()
		s.s = nil
	}
}
func (s *fileSession) exchange(ctx context.Context, q fileRequest, data []byte) (reply fileReply, err error) {
	if s.s != nil && s.route == "relay" && time.Since(s.opened) > 30*time.Second {
		s.close()
	}
	if s.s == nil {
		if !s.legacy {
			s.s, s.route, err = s.n.openProtocol(ctx, s.p, fileSessionProtocol)
			if err != nil {
				s.legacy = true
			}
		}
		if s.legacy {
			s.s, s.route, err = s.n.openProtocol(ctx, s.p, fileProtocol)
		}
		if err != nil {
			return reply, err
		}
		s.opened = time.Now()
	}
	stream := s.s
	stop := context.AfterFunc(ctx, func() { stream.Reset() })
	defer stop()
	defer func() {
		if err != nil {
			stream.Reset()
			s.s = nil
		} else if s.legacy {
			s.close()
		}
	}()
	stream.SetDeadline(deadline(ctx, 90*time.Second))
	if err = wire.Write(stream, q); err != nil {
		return
	}
	if err = wire.Read(stream, &reply); err != nil {
		return
	}
	if reply.ID != q.ID {
		return reply, errors.New("FILE_ACK_INVALID")
	}
	if q.Action == "chunk" && reply.Status == "ready" {
		if reply.Offset != q.Offset {
			return reply, errors.New("FILE_ACK_INVALID")
		}
		s.n.meterStart("out", s.p.String(), q.ID, q.Offset)
		for len(data) > 0 {
			var written int
			written, err = stream.Write(data)
			s.n.meterBytes("out", s.p.String(), q.ID, written)
			if err != nil {
				return
			}
			if written == 0 {
				return reply, io.ErrShortWrite
			}
			data = data[written:]
		}
		if err = wire.Read(stream, &reply); err != nil {
			return
		}
		if reply.ID != q.ID {
			return reply, errors.New("FILE_ACK_INVALID")
		}
	}
	return
}
func (n *Node) transferFile(ctx context.Context, r *FileRecord, p peer.ID, f *os.File) error {
	session := &fileSession{n: n, p: p}
	defer session.close()
	buf := make([]byte, FileChunk)
	failures := 0
	needStatus := true
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		q := fileRequest{Version: 2, ID: r.ID, Name: r.Name, Size: r.Size, SHA: r.SHA, Offset: r.Progress}
		var data []byte
		switch {
		case needStatus:
			q.Action = "status"
			q.Offset = 0
		case r.Progress == r.Size:
			q.Action = "finish"
		default:
			q.Action = "chunk"
			q.Length = min(int64(FileChunk), r.Size-r.Progress)
			data = buf[:int(q.Length)]
			if _, e := f.ReadAt(data, r.Progress); e != nil {
				return e
			}
			hash := sha256.Sum256(data)
			q.ChunkSHA = hex.EncodeToString(hash[:])
			r.Status = "sending"
		}
		reply, e := session.exchange(ctx, q, data)
		r.Route = session.route
		if e != nil {
			failures++
			r.Status = "reconnecting"
			r.Error = "NETWORK_RECONNECT"
			if e = n.saveFileRecord(*r); e != nil {
				return e
			}
			if failures >= 8 {
				return errors.New("NETWORK_UNAVAILABLE: use resume")
			}
			needStatus = true
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(min(failures, 5)) * time.Second):
			}
			continue
		}
		if reply.Offset < 0 || reply.Offset > r.Size {
			return errors.New("INVALID_REMOTE_OFFSET")
		}
		switch reply.Status {
		case "stored":
			if reply.Offset != r.Size {
				return errors.New("FILE_ACK_INVALID")
			}
			r.Progress = r.Size
			r.Status = "delivered"
			r.Error = ""
			return n.saveFileRecord(*r)
		case "resume":
			if q.Action != "status" {
				return errors.New("FILE_ACK_INVALID")
			}
			r.Progress = reply.Offset
			needStatus = false
			r.Status = "sending"
		case "accepted":
			if q.Action != "chunk" || reply.Offset != q.Offset+q.Length {
				return errors.New("FILE_ACK_INVALID")
			}
			r.Progress = reply.Offset
			needStatus = false
			r.Status = "sending"
		case "OFFSET_MISMATCH":
			r.Progress = reply.Offset
			needStatus = true
		default:
			return errors.New(reply.Status)
		}
		failures = 0
		r.Error = ""
		if e = n.saveFileRecord(*r); e != nil {
			return e
		}
	}
}
