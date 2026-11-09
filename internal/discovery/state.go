package discovery

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fuse/internal/securefs"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/peer"
)

type Sequence struct {
	mu   sync.Mutex
	path string
	last uint64
}
type sequenceFile struct {
	Version int    `json:"version"`
	Seq     uint64 `json:"seq"`
}

func InitSequence(dir string) error {
	return securefs.WriteJSON(filepath.Join(dir, "state.json"), sequenceFile{Version: 1})
}
func LoadSequence(dir string) (*Sequence, error) {
	p := filepath.Join(dir, "state.json")
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var s sequenceFile
	if e = wire.Decode(b, &s); e != nil || s.Version != 1 {
		return nil, errors.New("corrupt state.json")
	}
	return &Sequence{path: p, last: s.Seq}, nil
}
func (s *Sequence) Next(now time.Time) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == math.MaxUint64 {
		return 0, errors.New("sequence overflow")
	}
	next := s.last + 1
	if t := now.UnixNano(); t > 0 && uint64(t) > next {
		next = uint64(t)
	}
	if e := securefs.WriteJSON(s.path, sequenceFile{1, next}); e != nil {
		return 0, e
	}
	s.last = next
	return next, nil
}

type Cached struct {
	Envelope []byte    `json:"envelope"`
	Seen     time.Time `json:"seen"`
}
type cacheFile struct {
	Version        int               `json:"version"`
	Contacts       map[string]Cached `json:"contacts"`
	Infrastructure []string          `json:"infrastructure"`
}
type Cache struct {
	mu        sync.Mutex
	path      string
	data      cacheFile
	validator Validator
}

func LoadCache(dir string, v Validator) (*Cache, error) {
	c := &Cache{path: filepath.Join(dir, "peer-cache.json"), data: cacheFile{Version: 1, Contacts: map[string]Cached{}, Infrastructure: []string{}}, validator: v}
	b, e := os.ReadFile(c.path)
	if errors.Is(e, os.ErrNotExist) {
		return c, nil
	}
	if e != nil {
		return nil, e
	}
	if len(b) > 64<<20 {
		return nil, errors.New("cache too large")
	}
	if e = wire.Decode(b, &c.data); e != nil {
		return nil, e
	}
	if c.data.Version != 1 || c.data.Contacts == nil {
		return nil, errors.New("invalid cache version")
	}
	for id, item := range c.data.Contacts {
		if e = v.Validate("/p2pm/"+id, item.Envelope); e != nil {
			return nil, e
		}
	}
	return c, nil
}
func (c *Cache) Get(id peer.ID) (Cached, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.data.Contacts[id.String()]
	return v, ok
}
func (c *Cache) Put(id peer.ID, b []byte) (Cached, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.validator.Validate(Key(id), b); e != nil {
		return Cached{}, e
	}
	old, exists := c.data.Contacts[id.String()]
	if exists {
		best, e := c.validator.Select(Key(id), [][]byte{old.Envelope, b})
		if e != nil {
			return old, e
		}
		if best == 0 {
			return old, nil
		}
	}
	item := Cached{append([]byte(nil), b...), time.Now().UTC()}
	c.data.Contacts[id.String()] = item
	if e := securefs.WriteJSON(c.path, c.data); e != nil {
		if exists {
			c.data.Contacts[id.String()] = old
		} else {
			delete(c.data.Contacts, id.String())
		}
		return Cached{}, e
	}
	return item, nil
}
func (c *Cache) Infrastructure() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.data.Infrastructure...)
}
func (c *Cache) RememberInfrastructure(addresses []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]bool{}
	for _, a := range c.data.Infrastructure {
		seen[a] = true
	}
	for _, a := range addresses {
		if !seen[a] && len(c.data.Infrastructure) < 64 {
			c.data.Infrastructure = append(c.data.Infrastructure, a)
			seen[a] = true
		}
	}
	return securefs.WriteJSON(c.path, c.data)
}
