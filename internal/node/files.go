package node

import (
	"context"
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"fuse/internal/messaging"
	"fuse/internal/securefs"
	"fuse/internal/wire"
	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const fileProtocol = "/p2p-messenger/file/2.0.0"
const fileSessionProtocol = "/p2p-messenger/file/2.1.0"
const FileChunk = 4 << 20 // A chunk bound, not a file size bound.
type FileRecord struct {
	Version     int     `json:"version"`
	ID          string  `json:"file_id"`
	Name        string  `json:"name"`
	Size        int64   `json:"size"`
	SHA         string  `json:"sha256"`
	Peer        string  `json:"peer_id"`
	Direction   string  `json:"direction"`
	Status      string  `json:"status"`
	Path        string  `json:"path"`
	Time        string  `json:"time"`
	Progress    int64   `json:"progress"`
	Route       string  `json:"route,omitempty"`
	Error       string  `json:"error,omitempty"`
	HashState   []byte  `json:"hash_state,omitempty"`
	Transferred int64   `json:"transferred,omitempty"`
	Speed       float64 `json:"bytes_per_second,omitempty"`
	ExportPath  string  `json:"export_path,omitempty"`
	ExportError string  `json:"export_error,omitempty"`
}
type fileRequest struct {
	Version  int    `json:"version"`
	ID       string `json:"file_id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	SHA      string `json:"sha256"`
	Action   string `json:"action"`
	Offset   int64  `json:"offset"`
	Length   int64  `json:"length"`
	ChunkSHA string `json:"chunk_sha256,omitempty"`
}
type fileReply struct {
	ID     string `json:"file_id"`
	Status string `json:"status"`
	Offset int64  `json:"offset"`
}

func validRequest(o fileRequest) bool {
	h, e := hex.DecodeString(o.SHA)
	return o.Version == 2 && messaging.ValidID(o.ID) && o.Size >= 0 && o.Offset >= 0 && o.Offset <= o.Size && o.Length >= 0 && o.Length <= FileChunk && o.Length <= o.Size-o.Offset && len(o.Name) > 0 && len(o.Name) <= 255 && utf8.ValidString(o.Name) && !strings.ContainsAny(o.Name, "/\\\x00\r\n") && e == nil && len(h) == 32
}
func (n *Node) fileRecordPath(direction, p, id string) string {
	h := sha256.Sum256([]byte(p))
	return filepath.Join(n.dir, "files", direction+"-"+hex.EncodeToString(h[:8])+"-"+id+".json")
}
func (n *Node) saveFileRecord(r FileRecord) error {
	n.fileStateMu.Lock()
	defer n.fileStateMu.Unlock()
	r.Transferred, r.Speed = 0, 0
	path := n.fileRecordPath(r.Direction, r.Peer, r.ID)
	if e := securefs.WriteJSON(path, r); e != nil {
		return e
	}
	if n.fileRecords == nil {
		n.fileRecords = map[string]FileRecord{}
	}
	n.fileRecords[path] = r
	return nil
}
func readFileRecord(path string) (FileRecord, error) {
	var r FileRecord
	if e := securefs.Check(path); e != nil {
		return r, e
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return r, e
	}
	if len(b) > 32<<10 {
		return r, errors.New("FILE_STATE_INVALID")
	}
	e = wire.Decode(b, &r)
	return r, e
}
func (n *Node) Files(p string) ([]FileRecord, error) {
	n.fileStateMu.Lock()
	defer n.fileStateMu.Unlock()
	out := []FileRecord{}
	for _, r := range n.fileRecords {
		if p == "" || r.Peer == p {
			r.HashState = nil
			n.addFileMeter(&r)
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time < out[j].Time })
	return out, nil
}
func (n *Node) receiveFile(s network.Stream) {
	n.mu.Lock()
	if n.closing {
		n.mu.Unlock()
		s.Reset()
		return
	}
	select {
	case n.fileGate <- struct{}{}:
	default:
		n.mu.Unlock()
		s.Reset()
		return
	}
	n.wg.Add(1)
	n.mu.Unlock()
	defer n.wg.Done()
	defer func() { <-n.fileGate }()
	defer s.Close()
	stop := context.AfterFunc(n.ctx, func() { s.Reset() })
	defer stop()
	s.SetDeadline(time.Now().Add(90 * time.Second))
	for {
		s.SetDeadline(time.Now().Add(90 * time.Second))
		var q fileRequest
		if wire.Read(s, &q) != nil || !validRequest(q) {
			s.Reset()
			return
		}
		n.receiveFileOperation(s, q)
		if s.Protocol() != fileSessionProtocol {
			return
		}
	}
}
func (n *Node) receiveFileOperation(s network.Stream, q fileRequest) {
	respond := func(status string, offset int64) { wire.Write(s, fileReply{q.ID, status, offset}) }
	p := s.Conn().RemotePeer()
	if !n.knownContact(p) {
		respond("CONTACT_REQUIRED", 0)
		return
	}
	stripe := sha256.Sum256([]byte(p.String() + q.ID))
	lock := &n.receiveFileMu[int(stripe[0])%len(n.receiveFileMu)]
	lock.Lock()
	n.setRoute(p, "connected", streamRoute(s))
	defer lock.Unlock()
	statePath := n.fileRecordPath("in", p.String(), q.ID)
	n.fileStateMu.Lock()
	r, exists := n.fileRecords[statePath]
	n.fileStateMu.Unlock()
	var e error
	if !exists {
		e = os.ErrNotExist
	}
	if os.IsNotExist(e) {
		h := sha256.Sum256([]byte(p.String()))
		r = FileRecord{Version: 2, ID: q.ID, Name: q.Name, Size: q.Size, SHA: q.SHA, Peer: p.String(), Direction: "in", Status: "receiving", Path: filepath.Join(n.dir, "files", hex.EncodeToString(h[:8])+"-"+q.ID+".bin"), Time: time.Now().UTC().Format(time.RFC3339Nano)}
		e = n.saveFileRecord(r)
	}
	if e != nil {
		respond("STORAGE_ERROR", 0)
		return
	}
	if r.ID != q.ID || r.Peer != p.String() || r.Name != q.Name || r.Size != q.Size || r.SHA != q.SHA {
		respond("FILE_ID_CONFLICT", 0)
		return
	}
	r.Route = streamRoute(s)
	if r.Status == "stored" {
		respond("stored", r.Progress)
		return
	}
	if q.Action == "status" {
		respond("resume", r.Progress)
		return
	}
	if q.Action == "finish" {
		if r.Progress != r.Size {
			respond("OFFSET_MISMATCH", r.Progress)
			return
		}
		h := sha256.New()
		if r.Progress > 0 {
			if e = h.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.HashState); e != nil {
				respond("STORAGE_ERROR", r.Progress)
				return
			}
		}
		if hex.EncodeToString(h.Sum(nil)) != r.SHA {
			respond("HASH_MISMATCH", r.Progress)
			return
		}
		if r.Size == 0 {
			f, e := securefs.OpenPrivateFile(r.Path, os.O_CREATE|os.O_WRONLY)
			if e != nil {
				respond("STORAGE_ERROR", 0)
				return
			}
			e = f.Sync()
			f.Close()
			if e != nil || securefs.Protect(r.Path) != nil {
				respond("STORAGE_ERROR", 0)
				return
			}
		}
		r.Status = "stored"
		r.HashState = nil
		if n.saveFileRecord(r) != nil {
			respond("STORAGE_ERROR", r.Progress)
			return
		}
		respond("stored", r.Progress)
		return
	}
	if q.Action != "chunk" || q.Offset != r.Progress || q.Length == 0 {
		respond("OFFSET_MISMATCH", r.Progress)
		return
	}
	sum, e := hex.DecodeString(q.ChunkSHA)
	if e != nil || len(sum) != 32 {
		respond("INVALID_CHUNK", r.Progress)
		return
	}
	f, e := securefs.OpenPrivateFile(r.Path, os.O_CREATE|os.O_RDWR)
	if e != nil {
		respond("STORAGE_ERROR", r.Progress)
		return
	}
	defer f.Close()
	if e = securefs.Protect(r.Path); e != nil {
		respond("STORAGE_ERROR", r.Progress)
		return
	}
	if e = f.Truncate(r.Progress); e != nil {
		respond("STORAGE_ERROR", r.Progress)
		return
	}
	if _, e = f.Seek(r.Progress, 0); e != nil {
		respond("STORAGE_ERROR", r.Progress)
		return
	}
	whole := sha256.New()
	if r.Progress > 0 {
		if e = whole.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.HashState); e != nil {
			respond("STORAGE_ERROR", r.Progress)
			return
		}
	}
	chunk := sha256.New()
	if e = wire.Write(s, fileReply{q.ID, "ready", r.Progress}); e != nil {
		return
	}
	n.meterStart("in", p.String(), q.ID, r.Progress)
	if _, e = io.CopyN(io.MultiWriter(f, whole, chunk), &meterReader{r: s, n: n, direction: "in", peer: p.String(), id: q.ID}, q.Length); e != nil {
		s.Reset()
		return
	}
	if hex.EncodeToString(chunk.Sum(nil)) != q.ChunkSHA {
		respond("HASH_MISMATCH", r.Progress)
		return
	}
	if e = f.Sync(); e != nil {
		respond("STORAGE_ERROR", r.Progress)
		return
	}
	r.HashState, e = whole.(encoding.BinaryMarshaler).MarshalBinary()
	if e != nil {
		respond("STORAGE_ERROR", r.Progress)
		return
	}
	r.Progress += q.Length
	if n.saveFileRecord(r) != nil {
		respond("STORAGE_ERROR", q.Offset)
		return
	}
	respond("accepted", r.Progress)
}
func (n *Node) SendFile(_ context.Context, p peer.ID, path string) (FileRecord, error) {
	return n.QueueFile(p, path, uuid.NewString())
}
func (n *Node) QueueFile(p peer.ID, path, id string) (FileRecord, error) {
	r := FileRecord{}
	if !messaging.ValidID(id) {
		return r, errors.New("INVALID_FILE_ID")
	}
	if !n.knownContact(p) {
		return r, errors.New("CONTACT_REQUIRED")
	}
	path, e := filepath.Abs(path)
	if e != nil {
		return r, e
	}
	f, e := os.Open(path)
	if e != nil {
		return r, e
	}
	st, e := f.Stat()
	f.Close()
	if e != nil {
		return r, e
	}
	if !st.Mode().IsRegular() {
		return r, errors.New("INVALID_FILE")
	}
	n.jobsMu.Lock()
	defer n.jobsMu.Unlock()
	n.fileStateMu.Lock()
	existing, exists := n.fileRecords[n.fileRecordPath("out", p.String(), id)]
	n.fileStateMu.Unlock()
	if exists {
		if existing.Path != path || existing.Size != st.Size() {
			return r, errors.New("FILE_ID_CONFLICT")
		}
		return existing, nil
	}
	r = FileRecord{Version: 2, ID: id, Name: filepath.Base(path), Size: st.Size(), Peer: p.String(), Direction: "out", Status: "queued", Path: path, Time: time.Now().UTC().Format(time.RFC3339Nano)}
	if len(r.Name) == 0 || len(r.Name) > 255 || !utf8.ValidString(r.Name) || strings.ContainsAny(r.Name, "/\\\x00\r\n") {
		return r, errors.New("INVALID_FILE_NAME")
	}
	if e = n.saveFileRecord(r); e != nil {
		return r, e
	}
	n.wakeFiles()
	return r, nil
}
func (n *Node) FileAction(id, action string) error {
	rows, e := n.Files("")
	if e != nil {
		return e
	}
	for _, r := range rows {
		if r.ID != id || r.Direction != "out" {
			continue
		}
		n.jobsMu.Lock()
		defer n.jobsMu.Unlock()
		n.fileStateMu.Lock()
		r = n.fileRecords[n.fileRecordPath("out", r.Peer, r.ID)]
		n.fileStateMu.Unlock()
		cancel := n.fileJobs[id]
		if action == "pause" {
			if cancel != nil {
				cancel()
				return nil
			}
			if r.Status != "delivered" {
				r.Status = "paused"
				return n.saveFileRecord(r)
			}
			return nil
		}
		if r.Status == "delivered" {
			return nil
		}
		if cancel != nil {
			return errors.New("FILE_BUSY")
		}
		r.Status = "queued"
		r.Error = ""
		return n.saveFileRecord(r)
	}
	return errors.New("FILE_NOT_FOUND")
}
func (n *Node) startFileJob(r FileRecord) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.closing {
		return errors.New("NODE_NOT_READY")
	}
	n.jobsMu.Lock()
	defer n.jobsMu.Unlock()
	if n.fileJobs == nil {
		n.fileJobs = map[string]context.CancelFunc{}
	}
	if _, ok := n.fileJobs[r.ID]; ok {
		return errors.New("FILE_BUSY")
	}
	if len(n.fileJobs) >= 2 {
		return errors.New("RESOURCE_LIMIT: two active file transfers")
	}
	n.fileStateMu.Lock()
	r = n.fileRecords[n.fileRecordPath("out", r.Peer, r.ID)]
	n.fileStateMu.Unlock()
	if r.Status != "queued" {
		return nil
	}
	r.Status = "preparing"
	if e := n.saveFileRecord(r); e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(n.ctx)
	n.fileJobs[r.ID] = cancel
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		defer cancel()
		defer func() { n.jobsMu.Lock(); delete(n.fileJobs, r.ID); n.jobsMu.Unlock(); n.wakeFiles() }()
		if e := n.runFile(ctx, &r); e != nil {
			r.Status = "paused"
			if n.ctx.Err() != nil {
				r.Status = "queued"
			}
			r.Error = e.Error()
			n.saveFileRecord(r)
		}
	}()
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func (n *Node) runFile(ctx context.Context, r *FileRecord) error {
	f, e := os.Open(r.Path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if st.Size() != r.Size {
		return errors.New("FILE_CHANGED")
	}
	r.Status = "preparing"
	r.Error = ""
	if e = n.saveFileRecord(*r); e != nil {
		return e
	}
	h := sha256.New()
	if _, e = io.CopyBuffer(h, contextReader{ctx, f}, make([]byte, 128<<10)); e != nil {
		return e
	}
	sha := hex.EncodeToString(h.Sum(nil))
	if r.SHA != "" && r.SHA != sha {
		return errors.New("FILE_CHANGED")
	}
	r.SHA = sha
	r.Status = "connecting"
	if e = n.saveFileRecord(*r); e != nil {
		return e
	}
	p, e := peer.Decode(r.Peer)
	if e != nil {
		return e
	}
	return n.transferFile(ctx, r, p, f)
}
func (n *Node) fileExchange(ctx context.Context, p peer.ID, q fileRequest, data []byte) (fileReply, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	s, route, e := n.openProtocol(ctx, p, fileProtocol)
	if e != nil {
		return fileReply{}, route, e
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Reset() })
	defer stop()
	s.SetDeadline(deadline(ctx, 90*time.Second))
	if e = wire.Write(s, q); e != nil {
		return fileReply{}, route, e
	}
	var r fileReply
	if e = wire.Read(s, &r); e != nil {
		return r, route, e
	}
	if r.ID != q.ID {
		return r, route, errors.New("FILE_ACK_INVALID")
	}
	if q.Action == "chunk" && r.Status == "ready" {
		for len(data) > 0 {
			written, err := s.Write(data)
			if err != nil {
				return r, route, err
			}
			if written == 0 {
				return r, route, io.ErrShortWrite
			}
			data = data[written:]
		}
		if e = wire.Read(s, &r); e != nil {
			return r, route, e
		}
		if r.ID != q.ID {
			return r, route, errors.New("FILE_ACK_INVALID")
		}
	}
	return r, route, nil
}
