package storage

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"fuse/internal/messaging"
	"fuse/internal/securefs"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/peer"
)

const MaxLine = 1 << 20

type Event struct {
	Version   int                `json:"schema_version"`
	Kind      string             `json:"event"`
	Direction string             `json:"direction"`
	Peer      string             `json:"peer_id"`
	ID        string             `json:"message_id"`
	LocalTime string             `json:"local_time"`
	Message   *messaging.Message `json:"message,omitempty"`
	Status    string             `json:"status"`
	Code      string             `json:"code,omitempty"`
	Route     string             `json:"route,omitempty"`
}
type entry struct {
	offset              int64
	length              int
	hash                [32]byte
	status, code, route string
	peer, direction, id string
	order               int
}
type Journal struct {
	mu     sync.Mutex
	f      *os.File
	index  map[string]*entry
	size   int64
	failed bool
	closed bool
}

func indexKey(dir, p, id string) string { return dir + ":" + p + ":" + id }
func Open(dir string) (*Journal, error) {
	if e := securefs.CheckFiles(dir, "messages.jsonl"); e != nil {
		return nil, e
	}
	p := filepath.Join(dir, "messages.jsonl")
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = securefs.Protect(p); e != nil {
		f.Close()
		return nil, e
	}
	j := &Journal{f: f, index: map[string]*entry{}}
	if e = j.recover(dir); e != nil {
		f.Close()
		return nil, e
	}
	return j, nil
}
func (j *Journal) recover(dir string) error {
	r := bufio.NewReaderSize(j.f, 64<<10)
	var offset int64
	for {
		b, e := readLine(r)
		if e == io.EOF && len(b) == 0 {
			break
		}
		if e == io.EOF {
			if e = securefs.AtomicWrite(filepath.Join(dir, fmt.Sprintf("messages.jsonl.tail-%d", time.Now().UnixNano())), b); e != nil {
				return e
			}
			if e = j.f.Truncate(offset); e != nil {
				return e
			}
			if e = j.f.Sync(); e != nil {
				return e
			}
			break
		}
		if e != nil {
			return e
		}
		var event Event
		if e = wire.Decode(bytes.TrimSuffix(b, []byte{'\n'}), &event); e != nil {
			return fmt.Errorf("STORAGE_ERROR: corrupt journal at byte %d", offset)
		}
		if e = j.apply(event, offset, len(b)); e != nil {
			return fmt.Errorf("STORAGE_ERROR: journal byte %d: %w", offset, e)
		}
		offset += int64(len(b))
	}
	j.size = offset
	if _, e := j.f.Seek(offset, io.SeekStart); e != nil {
		return e
	}
	for k, v := range j.index {
		if v.status == "pending" {
			ev, e := j.read(v)
			if e != nil {
				return e
			}
			ev.Kind = "status"
			ev.Message = nil
			ev.Status = "delivery_unknown"
			ev.Code = "INTERRUPTED"
			ev.LocalTime = time.Now().UTC().Format(time.RFC3339Nano)
			if e = j.append(ev); e != nil {
				return e
			}
			j.index[k].status = "delivery_unknown"
		}
	}
	return nil
}
func readLine(r *bufio.Reader) ([]byte, error) {
	var b []byte
	for {
		part, e := r.ReadSlice('\n')
		if len(b)+len(part) > MaxLine {
			return nil, errors.New("journal line exceeds 1 MiB")
		}
		b = append(b, part...)
		if e == bufio.ErrBufferFull {
			continue
		}
		return b, e
	}
}
func (j *Journal) apply(e Event, offset int64, length int) error {
	if e.Version != 1 || (e.Direction != "in" && e.Direction != "out") || !messaging.ValidID(e.ID) {
		return errors.New("invalid journal identity/schema")
	}
	if _, err := peer.Decode(e.Peer); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, e.LocalTime); err != nil {
		return err
	}
	k := indexKey(e.Direction, e.Peer, e.ID)
	if e.Kind == "message" {
		if e.Message == nil || e.Message.ID != e.ID || e.Message.Validate() != nil {
			return errors.New("invalid journal message")
		}
		if _, ok := j.index[k]; ok {
			return errors.New("duplicate journal message")
		}
		if (e.Direction == "in" && e.Status != "stored") || (e.Direction == "out" && e.Status != "pending") {
			return errors.New("invalid initial status")
		}
		j.index[k] = &entry{offset: offset, length: length, hash: e.Message.Hash(), status: e.Status, order: len(j.index), peer: e.Peer, direction: e.Direction, id: e.ID}
		return nil
	}
	if e.Kind != "status" || e.Message != nil || e.Direction != "out" {
		return errors.New("invalid journal event")
	}
	v, ok := j.index[k]
	if !ok {
		return errors.New("orphan status event")
	}
	switch e.Status {
	case "pending", "delivered", "not_sent", "delivery_unknown", "rejected":
	default:
		return errors.New("invalid delivery status")
	}
	v.status, v.code, v.route = e.Status, e.Code, e.Route
	return nil
}
func (j *Journal) append(e Event) error {
	if j.failed || j.closed {
		return errors.New("STORAGE_ERROR: journal unavailable")
	}
	b, err := jsonBytes(e)
	if err != nil {
		return err
	}
	if len(b) > MaxLine {
		return errors.New("journal event too large")
	}
	n, err := j.f.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = j.f.Sync()
	}
	if err != nil {
		j.failed = true
		return errors.New("STORAGE_ERROR: durable append failed")
	}
	if err = j.apply(e, j.size, len(b)); err != nil {
		j.failed = true
		return err
	}
	j.size += int64(len(b))
	return nil
}
func (j *Journal) read(v *entry) (Event, error) {
	b := make([]byte, v.length)
	if _, e := j.f.ReadAt(b, v.offset); e != nil {
		return Event{}, e
	}
	var e Event
	if err := wire.Decode(bytes.TrimSpace(b), &e); err != nil {
		return e, err
	}
	e.Status, e.Code, e.Route = v.status, v.code, v.route
	return e, nil
}
func (j *Journal) Receive(p peer.ID, m messaging.Message) (string, error) {
	return j.ReceiveLimited(p, m, nil)
}
func (j *Journal) ReceiveLimited(p peer.ID, m messaging.Message, allowNew func() bool) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if e := m.Validate(); e != nil {
		return "", e
	}
	k := indexKey("in", p.String(), m.ID)
	if v, ok := j.index[k]; ok {
		if v.hash != m.Hash() {
			return "", errors.New("MESSAGE_ID_CONFLICT")
		}
		return "duplicate", nil
	}
	if allowNew != nil && !allowNew() {
		return "", errors.New("RATE_LIMITED")
	}
	e := Event{Version: 1, Kind: "message", Direction: "in", Peer: p.String(), ID: m.ID, LocalTime: time.Now().UTC().Format(time.RFC3339Nano), Message: &m, Status: "stored"}
	if err := j.append(e); err != nil {
		return "", err
	}
	return "stored", nil
}
func (j *Journal) Pending(p peer.ID, m messaging.Message) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if e := m.Validate(); e != nil {
		return e
	}
	if _, e := peer.Decode(p.String()); e != nil {
		return e
	}
	if _, exists := j.index[indexKey("out", p.String(), m.ID)]; exists {
		return errors.New("MESSAGE_ID_CONFLICT")
	}
	return j.append(Event{Version: 1, Kind: "message", Direction: "out", Peer: p.String(), ID: m.ID, LocalTime: time.Now().UTC().Format(time.RFC3339Nano), Message: &m, Status: "pending"})
}
func (j *Journal) Status(p peer.ID, id, status, code, route string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, exists := j.index[indexKey("out", p.String(), id)]; !exists {
		return errors.New("MESSAGE_NOT_FOUND")
	}
	switch status {
	case "pending", "delivered", "not_sent", "delivery_unknown", "rejected":
	default:
		return errors.New("INVALID_STATUS")
	}
	return j.append(Event{Version: 1, Kind: "status", Direction: "out", Peer: p.String(), ID: id, LocalTime: time.Now().UTC().Format(time.RFC3339Nano), Status: status, Code: code, Route: route})
}
func (j *Journal) Outgoing(id string) (Event, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, v := range j.index {
		if v.direction == "out" && v.id == id {
			return j.read(v)
		}
	}
	return Event{}, errors.New("MESSAGE_NOT_FOUND")
}

// History snapshots only offsets/status metadata, then emits bounded individual rows.
func (j *Journal) History(p string, limit int, emit func(Event) error) error {
	j.mu.Lock()
	entries := make([]entry, 0)
	for _, v := range j.index {
		if p == "" || v.peer == p {
			entries = append(entries, *v)
		}
	}
	j.mu.Unlock()
	sort.Slice(entries, func(i, k int) bool { return entries[i].order < entries[k].order })
	if limit > 0 && len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	for _, v := range entries {
		j.mu.Lock()
		e, err := j.read(&v)
		j.mu.Unlock()
		if err != nil {
			return err
		}
		if err = emit(e); err != nil {
			return err
		}
	}
	return nil
}
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.f.Close()
}
