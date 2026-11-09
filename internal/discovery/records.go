package discovery

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"strings"

	netstack "fuse/internal/network"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/record"
	ma "github.com/multiformats/go-multiaddr"
)

const MaxEnvelope = 8 << 10

func Key(id peer.ID) string { return "/p2pm/" + id.String() }
func ParseID(s string) (peer.ID, error) {
	id, e := peer.Decode(s)
	if e != nil {
		return "", errors.New("INVALID_PEER_ID")
	}
	return id, nil
}

type Validator struct {
	AllowPrivate bool
	Conflict     func()
}

func (v Validator) Validate(key string, value []byte) error { _, _, e := v.Read(key, value); return e }
func (v Validator) Read(key string, value []byte) (*record.Envelope, *peer.PeerRecord, error) {
	if !strings.HasPrefix(key, "/p2pm/") || len(value) == 0 || len(value) > MaxEnvelope {
		return nil, nil, errors.New("invalid contact key/size")
	}
	id, e := peer.Decode(strings.TrimPrefix(key, "/p2pm/"))
	if e != nil || Key(id) != key {
		return nil, nil, errors.New("noncanonical contact key")
	}
	env, r, e := record.ConsumeEnvelope(value, peer.PeerRecordEnvelopeDomain)
	if e != nil {
		return nil, nil, e
	}
	pr, ok := r.(*peer.PeerRecord)
	if !ok {
		return nil, nil, errors.New("unexpected envelope payload")
	}
	signer, e := peer.IDFromPublicKey(env.PublicKey)
	if e != nil || signer != id || pr.PeerID != id || pr.Seq == 0 || len(pr.Addrs) > 8 {
		return nil, nil, errors.New("invalid contact identity/sequence/addresses")
	}
	for _, a := range pr.Addrs {
		if e = v.Address(a); e != nil {
			return nil, nil, e
		}
	}
	return env, pr, nil
}
func (v Validator) Address(a ma.Multiaddr) error {
	if len(a.Bytes()) > 512 {
		return errors.New("address too long")
	}
	p := a.Protocols()
	if len(p) != 2 && len(p) != 4 {
		return errors.New("unsupported route shape")
	}
	if p[0].Code != ma.P_IP4 && p[0].Code != ma.P_IP6 && p[0].Code != ma.P_DNS4 && p[0].Code != ma.P_DNS6 {
		return errors.New("unsupported address family")
	}
	if p[1].Code != ma.P_TCP {
		return errors.New("TCP required")
	}
	port, _ := a.ValueForProtocol(ma.P_TCP)
	if port == "0" {
		return errors.New("invalid port")
	}
	if len(p) == 4 {
		if p[2].Code != ma.P_P2P || p[3].Code != ma.P_CIRCUIT {
			return errors.New("only one relay hop supported")
		}
		id, _ := a.ValueForProtocol(ma.P_P2P)
		if _, e := peer.Decode(id); e != nil {
			return e
		}
	}
	if !v.AllowPrivate && (p[0].Code == ma.P_IP4 || p[0].Code == ma.P_IP6) {
		s, _ := a.ValueForProtocol(p[0].Code)
		if !netstack.PublicIP(net.ParseIP(s)) {
			return errors.New("nonpublic address")
		}
	}
	return nil
}
func (v Validator) Select(key string, values [][]byte) (int, error) {
	best := -1
	var seq uint64
	var hash [32]byte
	for i, b := range values {
		env, p, e := v.Read(key, b)
		if e != nil {
			continue
		}
		h := sha256.Sum256(env.RawPayload)
		if best >= 0 && p.Seq == seq && h != hash && v.Conflict != nil {
			v.Conflict()
		}
		if best < 0 || p.Seq > seq || (p.Seq == seq && bytes.Compare(h[:], hash[:]) > 0) {
			best, seq, hash = i, p.Seq, h
		}
	}
	if best < 0 {
		return 0, errors.New("no valid contact records")
	}
	return best, nil
}
func Seal(key crypto.PrivKey, seq uint64, addresses []ma.Multiaddr, v Validator) ([]byte, error) {
	id, e := peer.IDFromPrivateKey(key)
	if e != nil {
		return nil, e
	}
	env, e := record.Seal(&peer.PeerRecord{PeerID: id, Seq: seq, Addrs: addresses}, key)
	if e != nil {
		return nil, e
	}
	b, e := env.Marshal()
	if e != nil {
		return nil, e
	}
	if e = v.Validate(Key(id), b); e != nil {
		return nil, fmt.Errorf("local contact invalid: %w", e)
	}
	return b, nil
}
