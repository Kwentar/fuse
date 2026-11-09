package node

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"fuse/internal/securefs"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

// The Android app calls this only after copying and hashing a document into Downloads.
// Metadata is persisted first so a crash can never lose the only complete copy.
func (n *Node) PublishFile(id, uri string) error {
	if runtime.GOOS != "android" || !strings.HasPrefix(uri, "content://") || len(uri) > 8192 {
		return errors.New("INVALID_EXPORT_URI")
	}
	rows, _ := n.Files("")
	for _, r := range rows {
		if r.ID != id || r.Direction != "in" {
			continue
		}
		if r.Status != "stored" {
			return errors.New("FILE_NOT_COMPLETE")
		}
		if r.ExportPath != "" {
			return nil
		}
		original := r.Path
		r.ExportPath = uri
		r.Path = uri
		r.ExportError = ""
		if e := n.saveFileRecord(r); e != nil {
			return e
		}
		base := filepath.Join(n.dir, "files") + string(os.PathSeparator)
		if strings.HasPrefix(original, base) {
			os.Remove(original)
		}
		return nil
	}
	return errors.New("FILE_NOT_FOUND")
}

func (n *Node) DownloadDir() string {
	n.downloadMu.Lock()
	defer n.downloadMu.Unlock()
	return n.downloadDir
}
func (n *Node) SetDownloadDir(path string) error {
	path, e := filepath.Abs(path)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(path, 0755); e != nil {
		return e
	}
	n.downloadMu.Lock()
	defer n.downloadMu.Unlock()
	if e = securefs.WriteJSON(filepath.Join(n.dir, "downloads.json"), map[string]string{"path": path}); e != nil {
		return e
	}
	n.downloadDir = path
	return nil
}
func (n *Node) loadDownloadDir() error {
	path := filepath.Join(n.dir, "downloads.json")
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = securefs.Check(path); e != nil {
		return e
	}
	var value map[string]string
	if e = json.Unmarshal(b, &value); e != nil {
		return e
	}
	n.downloadDir = value["path"]
	return nil
}
func safeDownloadName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune("<>:\"/\\|?*", r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "file"
	}
	for len(name) > 200 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		name = "_" + name
	}
	return name
}
func (n *Node) exportIncoming(r FileRecord, dir string) error {
	stripe := sha256.Sum256([]byte(r.Peer + r.ID))
	lock := &n.receiveFileMu[int(stripe[0])%len(n.receiveFileMu)]
	lock.Lock()
	defer lock.Unlock()
	n.fileStateMu.Lock()
	r = n.fileRecords[n.fileRecordPath("in", r.Peer, r.ID)]
	n.fileStateMu.Unlock()
	if r.Status != "stored" || r.ExportPath != "" {
		return nil
	}
	if e := os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	name := safeDownloadName(r.Name)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 10000; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i+1, ext)
		}
		target := filepath.Join(dir, candidate)
		// Atomic no-overwrite publication and no second data copy on the same filesystem.
		e := os.Link(r.Path, target)
		if os.IsExist(e) {
			continue
		}
		if e != nil {
			out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if os.IsExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			in, err := os.Open(r.Path)
			if err == nil {
				_, err = io.CopyBuffer(out, contextReader{n.ctx, in}, make([]byte, 128<<10))
				in.Close()
			}
			if err == nil {
				err = out.Sync()
			}
			closeErr := out.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				os.Remove(target)
				return err
			}
		}
		original := r.Path
		r.Path = target
		r.ExportPath = target
		r.ExportError = ""
		if e = n.saveFileRecord(r); e != nil {
			os.Remove(target)
			return e
		}
		// The durable record now points to Downloads; removing the private link does not lose data.
		os.Remove(original)
		return nil
	}
	return errors.New("DOWNLOAD_NAME_COLLISIONS")
}
func (n *Node) downloadLoop() {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-timer.C:
			dir := n.DownloadDir()
			if dir == "" {
				continue
			}
			rows, _ := n.Files("")
			for _, r := range rows {
				if n.ctx.Err() != nil {
					return
				}
				if r.Direction != "in" || r.Status != "stored" || r.ExportPath != "" {
					continue
				}
				if e := n.exportIncoming(r, dir); e != nil && r.ExportError != e.Error() {
					r.ExportError = e.Error()
					n.saveFileRecord(r)
				}
			}
		}
	}
}
