package node

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (n *Node) loadFiles() error {
	n.fileRecords = map[string]FileRecord{}
	entries, e := os.ReadDir(filepath.Join(n.dir, "files"))
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(n.dir, "files", entry.Name())
		r, e := readFileRecord(path)
		if e != nil {
			return e
		}
		if r.Direction == "out" && r.Status != "delivered" && r.Status != "paused" {
			r.Status = "queued"
		}
		n.fileRecords[path] = r
	}
	return nil
}
func (n *Node) fileQueueLoop() {
	timer := time.NewTicker(500 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-timer.C:
		case <-n.fileQueueWake:
		}
		rows, _ := n.Files("")
		for _, r := range rows {
			if r.Direction == "out" && r.Status == "queued" {
				if n.startFileJob(r) != nil {
					break
				}
			}
		}
	}
}
func (n *Node) wakeFiles() {
	select {
	case n.fileQueueWake <- struct{}{}:
	default:
	}
}

type fileMeter struct {
	offset            int64
	windowBytes       int64
	windowStart, time time.Time
	rate              float64
}

func meterKey(direction, peer, id string) string { return direction + ":" + peer + ":" + id }
func (n *Node) meterStart(direction, peer, id string, offset int64) {
	n.fileMeterMu.Lock()
	defer n.fileMeterMu.Unlock()
	if n.fileMeters == nil {
		n.fileMeters = map[string]*fileMeter{}
	}
	key := meterKey(direction, peer, id)
	m := n.fileMeters[key]
	if m == nil {
		m = &fileMeter{windowStart: time.Now()}
		n.fileMeters[key] = m
	}
	m.offset = offset
}
func (n *Node) meterBytes(direction, peer, id string, count int) {
	n.fileMeterMu.Lock()
	defer n.fileMeterMu.Unlock()
	if direction == "in" {
		n.fileDownloaded += int64(count)
	} else {
		n.fileUploaded += int64(count)
	}
	m := n.fileMeters[meterKey(direction, peer, id)]
	if m == nil {
		return
	}
	now := time.Now()
	m.offset += int64(count)
	m.windowBytes += int64(count)
	m.time = now
	if elapsed := now.Sub(m.windowStart).Seconds(); elapsed >= 0.5 {
		m.rate = float64(m.windowBytes) / elapsed
		m.windowBytes = 0
		m.windowStart = now
	}
}
func (n *Node) addFileMeter(r *FileRecord) {
	r.Transferred = r.Progress
	n.fileMeterMu.Lock()
	defer n.fileMeterMu.Unlock()
	m := n.fileMeters[meterKey(r.Direction, r.Peer, r.ID)]
	if m == nil {
		return
	}
	if r.Status == "sending" || r.Status == "receiving" {
		r.Transferred = min(r.Size, max(r.Progress, m.offset))
		if time.Since(m.time) < 2*time.Second {
			r.Speed = m.rate
		}
	}
}

type meterReader struct {
	r                   io.Reader
	n                   *Node
	direction, peer, id string
}

func (r *meterReader) Read(b []byte) (int, error) {
	count, e := r.r.Read(b)
	r.n.meterBytes(r.direction, r.peer, r.id, count)
	return count, e
}

type TransferStats struct {
	Uploaded   int64 `json:"uploaded"`
	Downloaded int64 `json:"downloaded"`
}

func (n *Node) TransferStats() TransferStats {
	n.fileMeterMu.Lock()
	defer n.fileMeterMu.Unlock()
	return TransferStats{n.fileUploaded, n.fileDownloaded}
}
