package identity

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"

	"fuse/internal/securefs"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
)

func Load(dir string) (crypto.PrivKey, error) {
	p := filepath.Join(dir, "identity.key")
	if e := securefs.Check(p); e != nil {
		return nil, errors.New("IDENTITY_ERROR: key missing, unreadable or unsafe; initialize with init")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	k, e := crypto.UnmarshalPrivateKey(b)
	if e != nil || k.Type() != crypto.Ed25519 {
		return nil, errors.New("IDENTITY_ERROR: invalid Ed25519 key; refusing replacement")
	}
	return k, nil
}
func Init(dir string) (peer.ID, error) {
	p := filepath.Join(dir, "identity.key")
	if _, e := os.Stat(p); e == nil {
		k, e := Load(dir)
		if e != nil {
			return "", e
		}
		return peer.IDFromPrivateKey(k)
	} else if !errors.Is(e, os.ErrNotExist) {
		return "", e
	}
	k, _, e := crypto.GenerateEd25519Key(rand.Reader)
	if e != nil {
		return "", e
	}
	b, e := crypto.MarshalPrivateKey(k)
	if e != nil {
		return "", e
	}
	if e = securefs.AtomicWrite(p, b); e != nil {
		return "", e
	}
	return peer.IDFromPrivateKey(k)
}
