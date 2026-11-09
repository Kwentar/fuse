package discovery

import (
	"bytes"
	"fuse/internal/securefs"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	ma "github.com/multiformats/go-multiaddr"
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestContactValidationSelection(t *testing.T) {
	k, _, _ := crypto.GenerateEd25519Key(nil)
	id, _ := peer.IDFromPrivateKey(k)
	other, _, _ := crypto.GenerateEd25519Key(nil)
	otherID, _ := peer.IDFromPrivateKey(other)
	v := Validator{}
	a := []ma.Multiaddr{ma.StringCast("/ip4/8.8.8.8/tcp/4001")}
	one, e := Seal(k, 1, a, v)
	if e != nil {
		t.Fatal(e)
	}
	two, _ := Seal(k, 2, a, v)
	if i, e := v.Select(Key(id), [][]byte{two, one}); e != nil || i != 0 {
		t.Fatal(i, e)
	}
	env, _ := record.Seal(&peer.PeerRecord{PeerID: id, Seq: 3, Addrs: a}, other)
	foreign, _ := env.Marshal()
	for _, b := range [][]byte{foreign, bytes.Repeat([]byte{0}, MaxEnvelope+1), append([]byte(nil), one[:len(one)/2]...)} {
		if v.Validate(Key(id), b) == nil {
			t.Fatal("invalid envelope accepted")
		}
	}
	if v.Validate(Key(otherID), one) == nil {
		t.Fatal("wrong key accepted")
	}
	if _, e = Seal(k, 0, a, v); e == nil {
		t.Fatal("zero sequence")
	}
	if _, e = Seal(k, 3, []ma.Multiaddr{ma.StringCast("/ip4/127.0.0.1/tcp/1")}, v); e == nil {
		t.Fatal("private route accepted")
	}
	if _, e = Seal(k, 3, []ma.Multiaddr{a[0].Encapsulate(ma.StringCast("/p2p/" + id.String()))}, v); e == nil {
		t.Fatal("trailing destination accepted")
	}
}

type wrongRecord struct {
	domain  string
	codec   []byte
	payload []byte
}

func (r *wrongRecord) Domain() string                 { return r.domain }
func (r *wrongRecord) Codec() []byte                  { return r.codec }
func (r *wrongRecord) MarshalRecord() ([]byte, error) { return r.payload, nil }
func (r *wrongRecord) UnmarshalRecord(b []byte) error { r.payload = append([]byte{}, b...); return nil }
func TestWrongDomainTypeAndAddressCount(t *testing.T) {
	k, _, _ := crypto.GenerateEd25519Key(nil)
	id, _ := peer.IDFromPrivateKey(k)
	pr := &peer.PeerRecord{PeerID: id, Seq: 1}
	payload, _ := pr.MarshalRecord()
	for _, r := range []*wrongRecord{{"different-domain", pr.Codec(), payload}, {pr.Domain(), []byte{0xff, 0xff, 0x01}, payload}} {
		env, e := record.Seal(r, k)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := env.Marshal()
		if (Validator{}).Validate(Key(id), b) == nil {
			t.Fatal("wrong type/domain accepted")
		}
	}
	addresses := make([]ma.Multiaddr, 9)
	for i := range addresses {
		addresses[i] = ma.StringCast("/ip4/8.8.8.8/tcp/4001")
	}
	if _, e := Seal(k, 1, addresses, Validator{}); e == nil {
		t.Fatal("too many addresses accepted")
	}
	a, _ := Seal(k, 2, []ma.Multiaddr{ma.StringCast("/ip4/8.8.8.8/tcp/1")}, Validator{})
	b, _ := Seal(k, 2, []ma.Multiaddr{ma.StringCast("/ip4/8.8.8.8/tcp/2")}, Validator{})
	conflicts := 0
	v := Validator{Conflict: func() { conflicts++ }}
	i, e := v.Select(Key(id), [][]byte{a, b})
	if e != nil {
		t.Fatal(e)
	}
	j, e := v.Select(Key(id), [][]byte{b, a})
	if e != nil || i == j || conflicts != 2 {
		t.Fatal("nondeterministic conflict selection")
	}
}
func TestPersistentSequenceAndCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if e := securefs.EnsureDir(dir); e != nil {
		t.Fatal(e)
	}
	if e := InitSequence(dir); e != nil {
		t.Fatal(e)
	}
	s, e := LoadSequence(dir)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Next(time.Now())
	if e != nil {
		t.Fatal(e)
	}
	s, e = LoadSequence(dir)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Next(time.Unix(0, 1))
	if e != nil || b <= a {
		t.Fatal(a, b, e)
	}
	s.last = math.MaxUint64
	if _, e = s.Next(time.Now()); e == nil {
		t.Fatal("overflow")
	}
	k, _, _ := crypto.GenerateEd25519Key(nil)
	id, _ := peer.IDFromPrivateKey(k)
	c, e := LoadCache(dir, Validator{})
	if e != nil {
		t.Fatal(e)
	}
	newer, _ := Seal(k, 5, nil, Validator{})
	older, _ := Seal(k, 2, nil, Validator{})
	if _, e = c.Put(id, newer); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Put(id, older); e != nil {
		t.Fatal(e)
	}
	c, e = LoadCache(dir, Validator{})
	if e != nil {
		t.Fatal(e)
	}
	got, _ := c.Get(id)
	_, pr, e := (Validator{}).Read(Key(id), got.Envelope)
	if e != nil || pr.Seq != 5 {
		t.Fatal(e)
	}
}
