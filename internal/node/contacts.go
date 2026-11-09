package node

import (
	"errors"
	"fuse/internal/securefs"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/peer"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type Contact struct {
	ID   string `json:"peer_id"`
	Name string `json:"name"`
}

func (n *Node) contacts() ([]Contact, error) {
	p := filepath.Join(n.dir, "contacts.json")
	if _, e := os.Stat(p); errors.Is(e, os.ErrNotExist) {
		return []Contact{}, nil
	}
	if e := securefs.Check(p); e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	if len(b) > 256<<10 {
		return nil, errors.New("CONTACT_LIMIT")
	}
	var c []Contact
	e = wire.Decode(b, &c)
	return c, e
}
func (n *Node) Contacts() ([]Contact, error) {
	n.contactMu.Lock()
	defer n.contactMu.Unlock()
	return n.contacts()
}
func (n *Node) AddContact(id peer.ID, name string) error {
	n.contactMu.Lock()
	defer n.contactMu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || !utf8.ValidString(name) {
		return errors.New("INVALID_NAME")
	}
	if id == n.Host.ID() {
		return errors.New("SELF_CONTACT")
	}
	c, e := n.contacts()
	if e != nil {
		return e
	}
	for i := range c {
		if c[i].ID == id.String() {
			c[i].Name = name
			return securefs.WriteJSON(filepath.Join(n.dir, "contacts.json"), c)
		}
	}
	if len(c) >= 500 {
		return errors.New("CONTACT_LIMIT")
	}
	return securefs.WriteJSON(filepath.Join(n.dir, "contacts.json"), append(c, Contact{id.String(), name}))
}
func (n *Node) knownContact(id peer.ID) bool {
	c, e := n.Contacts()
	if e != nil {
		return false
	}
	for _, v := range c {
		if v.ID == id.String() {
			return true
		}
	}
	return false
}
