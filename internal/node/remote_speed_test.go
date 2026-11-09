package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fuse/internal/identity"
	netstack "fuse/internal/network"
	"fuse/internal/wire"
	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"os"
	"testing"
	"time"
)

// Explicit opt-in diagnostic. Uses the authorized local profile's identity in memory,
// never prints key material and does not publish a competing DHT record.
func TestAuthorizedRemoteSpeed(t *testing.T) {
	dir := os.Getenv("P2PM_SPEED_PROFILE")
	if dir == "" {
		t.Skip("explicit remote test only")
	}
	remote, e := peer.Decode(os.Getenv("P2PM_SPEED_PEER"))
	if e != nil {
		t.Fatal(e)
	}
	addr, e := ma.NewMultiaddr(os.Getenv("P2PM_SPEED_ADDR"))
	if e != nil {
		t.Fatal(e)
	}
	if !sameLAN(addr) {
		t.Fatal("diagnostic restricted to local subnet")
	}
	key, e := identity.Load(dir)
	if e != nil {
		t.Fatal(e)
	}
	h, e := netstack.NewHostWithPolicy(key, nil, nil, false, netstack.AddressGate{AllowLAN: func(p peer.ID, a ma.Multiaddr) bool { return p == remote && sameLAN(a) }})
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if e = h.Connect(ctx, peer.AddrInfo{ID: remote, Addrs: []ma.Multiaddr{addr}}); e != nil {
		t.Fatal(e)
	}
	block := make([]byte, FileChunk)
	for i := range block {
		block[i] = byte(i % 251)
	}
	hash := sha256.New()
	for i := 0; i < 16; i++ {
		hash.Write(block)
	}
	q := fileRequest{Version: 2, ID: uuid.NewString(), Name: "p2pm-lan-probe-64MiB.bin", Size: 64 << 20, SHA: hex.EncodeToString(hash.Sum(nil)), Action: "status"}
	ch := sha256.Sum256(block)
	q.ChunkSHA = hex.EncodeToString(ch[:])
	start := time.Now()
	var totalWrite, totalACK time.Duration
	for step := 0; step < 18; step++ {
		s, e := h.NewStream(ctx, remote, fileProtocol)
		if e != nil {
			t.Fatal(e)
		}
		s.SetDeadline(time.Now().Add(45 * time.Second))
		if step == 0 {
			q.Action = "status"
			q.Length = 0
		} else if step == 17 {
			q.Action = "finish"
			q.Length = 0
			q.Offset = q.Size
		} else {
			q.Action = "chunk"
			q.Length = FileChunk
			q.Offset = int64(step-1) * FileChunk
		}
		if e = wire.Write(s, q); e != nil {
			t.Fatal(e)
		}
		var r fileReply
		if e = wire.Read(s, &r); e != nil {
			t.Fatal(e)
		}
		if q.Action == "chunk" {
			if r.Status != "ready" {
				t.Fatal(r)
			}
			began := time.Now()
			remaining := block
			for len(remaining) > 0 {
				n, e := s.Write(remaining)
				if e != nil {
					t.Fatal(e)
				}
				remaining = remaining[n:]
			}
			writeTime := time.Since(began)
			began = time.Now()
			if e = wire.Read(s, &r); e != nil {
				t.Fatal(e)
			}
			ackTime := time.Since(began)
			if r.Status != "accepted" {
				t.Fatal(r)
			}
			totalWrite += writeTime
			totalACK += ackTime
			t.Logf("chunk %02d: socket write %.3fs, ACK wait %.3fs", step, writeTime.Seconds(), ackTime.Seconds())
		} else if step == 0 && r.Status != "resume" || step == 17 && r.Status != "stored" {
			t.Fatal(r)
		}
		s.Close()
	}
	t.Logf("LAN 64 MiB: %.2fs, %.2f MiB/s; socket writes %.2fs, ACK waits %.2fs", time.Since(start).Seconds(), 64/time.Since(start).Seconds(), totalWrite.Seconds(), totalACK.Seconds())
}
