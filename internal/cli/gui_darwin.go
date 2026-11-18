package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"fuse/internal/securefs"
	"golang.org/x/sys/unix"
)

// Back up a quiescent profile before the new queue/export metadata is written.
// APFS clones are independent copy-on-write files, not hardlinks to live data.
func prepareGUIProfile(dir string) error {
	lock, e := securefs.Acquire(dir)
	if e != nil {
		return e
	}
	defer lock.Close()
	marker := filepath.Join(dir, "macos-version.json")
	if b, e := os.ReadFile(marker); e == nil {
		if e = securefs.Check(marker); e != nil {
			return e
		}
		var saved struct {
			Version string `json:"version"`
			Backup  string `json:"backup"`
		}
		if e = json.Unmarshal(b, &saved); e != nil {
			return fmt.Errorf("MIGRATION_ERROR: invalid marker: %w", e)
		}
		if saved.Version == Version {
			return nil
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	base := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+" Backups")
	if e = securefs.EnsureDir(base); e != nil {
		return e
	}
	backup, e := os.MkdirTemp(base, time.Now().Format("20060102-150405")+"-before-"+Version+"-")
	if e != nil {
		return e
	}
	e = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == dir {
			return nil
		}
		if entry.Name() == "control.sock" || entry.Name() == "node.lock" {
			return nil
		}
		rel, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		target := filepath.Join(backup, rel)
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("MIGRATION_ERROR: profile contains a symbolic link")
		}
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("MIGRATION_ERROR: unexpected profile file type")
		}
		if e = unix.Clonefile(path, target, unix.CLONE_NOFOLLOW); e == nil {
			return os.Chmod(target, 0600)
		}
		in, e := os.Open(path)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = io.CopyBuffer(out, in, make([]byte, 128<<10))
		if e == nil {
			e = out.Sync()
		}
		closeErr := out.Close()
		if e == nil {
			e = closeErr
		}
		return e
	})
	if e != nil {
		return fmt.Errorf("MIGRATION_ERROR: backup incomplete, profile unchanged: %w", e)
	}
	return securefs.WriteJSON(marker, map[string]string{"version": Version, "backup": backup})
}
