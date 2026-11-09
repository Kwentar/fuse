package appinit

import (
	"errors"
	"fuse/internal/config"
	"fuse/internal/discovery"
	"fuse/internal/identity"
	"fuse/internal/securefs"
	"github.com/libp2p/go-libp2p/core/peer"
	"os"
	"path/filepath"
)

func Init(dir string, c config.Client) (peer.ID, error) {
	if e := securefs.EnsureDir(dir); e != nil {
		return "", e
	}
	lock, e := securefs.Acquire(dir)
	if e != nil {
		return "", e
	}
	defer lock.Close()
	if e = securefs.CheckFiles(dir, "identity.key", "state.json", "config.json", "peer-cache.json", "messages.jsonl"); e != nil {
		return "", e
	}
	if _, e = os.Stat(filepath.Join(dir, "identity.key")); e == nil {
		if _, e = discovery.LoadSequence(dir); e != nil {
			return "", errors.New("IDENTITY_ERROR: key exists without valid state; restore the whole data directory")
		}
		return identity.Init(dir)
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	if e = c.Validate(); e != nil {
		return "", e
	}
	if e = discovery.InitSequence(dir); e != nil {
		return "", e
	}
	if e = securefs.WriteJSON(filepath.Join(dir, "config.json"), c); e != nil {
		return "", e
	}
	return identity.Init(dir)
}
