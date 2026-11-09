// Package logging accepts metadata only; chat payloads have no logging API.
package logging

import (
	"encoding/json"
	"fmt"
	"fuse/internal/securefs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const MaxSize = 10 << 20

type Log struct {
	mu   sync.Mutex
	dir  string
	f    *os.File
	size int64
}

func Open(dataDir string) (*Log, error) {
	dir := filepath.Join(dataDir, "logs")
	if e := securefs.EnsureDir(dir); e != nil {
		return nil, e
	}
	l := &Log{dir: dir}
	if e := l.open(); e != nil {
		return nil, e
	}
	return l, nil
}
func (l *Log) open() error {
	p := filepath.Join(l.dir, "node.jsonl")
	if e := securefs.CheckFiles(l.dir, "node.jsonl", "node.1.jsonl", "node.2.jsonl"); e != nil {
		return e
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	if e = securefs.Protect(p); e != nil {
		f.Close()
		return e
	}
	s, e := f.Stat()
	if e != nil {
		f.Close()
		return e
	}
	l.f, l.size = f, s.Size()
	return nil
}
func (l *Log) Event(code, peer string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, e := json.Marshal(struct {
		Time string `json:"time"`
		Code string `json:"code"`
		Peer string `json:"peer_id,omitempty"`
	}{time.Now().UTC().Format(time.RFC3339Nano), code, peer})
	if e != nil {
		return e
	}
	b = append(b, '\n')
	if l.size+int64(len(b)) > MaxSize {
		if e = l.f.Close(); e != nil {
			return e
		}
		for i := 2; i >= 1; i-- {
			dst := filepath.Join(l.dir, fmt.Sprintf("node.%d.jsonl", i))
			if e = os.Remove(dst); e != nil && !os.IsNotExist(e) {
				return e
			}
			src := filepath.Join(l.dir, "node.jsonl")
			if i > 1 {
				src = filepath.Join(l.dir, fmt.Sprintf("node.%d.jsonl", i-1))
			}
			if e = os.Rename(src, dst); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
		if e = l.open(); e != nil {
			return e
		}
	}
	n, e := l.f.Write(b)
	l.size += int64(n)
	return e
}
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}
