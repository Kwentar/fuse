package securefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func EnsureDir(path string) error {
	if s, err := os.Lstat(path); err == nil {
		if !s.IsDir() || s.Mode()&os.ModeSymlink != 0 {
			return errors.New("PERMISSION_ERROR: unsafe data directory")
		}
		return Check(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return Protect(path)
}

func AtomicWrite(path string, data []byte) error {
	if err := Check(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = Protect(tmp); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return replace(tmp, path)
}

func WriteJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return AtomicWrite(path, append(b, '\n'))
}

func CheckFiles(dir string, names ...string) error {
	if err := Check(dir); err != nil {
		return err
	}
	for _, name := range names {
		p := filepath.Join(dir, name)
		if _, e := os.Lstat(p); errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e := Check(p); e != nil {
			return fmt.Errorf("PERMISSION_ERROR: %s: %w", name, e)
		}
	}
	return nil
}
