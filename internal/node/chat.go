package node

import (
	"context"
	"errors"
	"strings"
	"time"

	"fuse/internal/config"
	"fuse/internal/messaging"
	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	ma "github.com/multiformats/go-multiaddr"
	"golang.org/x/time/rate"
)

type Delivery struct {
	ID     string `json:"message_id"`
	Peer   string `json:"peer_id"`
	Status string `json:"status"`
	Route  string `json:"route,omitempty"`
	Code   string `json:"code,omitempty"`
	ACK    string `json:"ack,omitempty"`
}

func deadline(ctx context.Context, d time.Duration) time.Time {
	end := time.Now().Add(d)
	if v, ok := ctx.Deadline(); ok && v.Before(end) {
		return v
	}
	return end
}
func (n *Node) receive(s network.Stream) {
	p := s.Conn().RemotePeer()
	if n.knownContact(p) {
		n.setRoute(p, "connected", streamRoute(s))
	}
	n.mu.Lock()
	now := time.Now()
	for id, b := range n.budgets {
		if b.active == 0 && now.Sub(b.last) > time.Minute {
			delete(n.budgets, id)
		}
	}
	budget := n.budgets[p]
	if budget == nil && len(n.budgets) < 1024 {
		budget = &senderBudget{limiter: rate.NewLimiter(10, 20)}
		n.budgets[p] = budget
	}
	if n.closing || budget == nil || n.active >= 32 || budget.active >= 2 {
		n.mu.Unlock()
		s.Reset()
		return
	}
	n.active++
	budget.active++
	budget.last = now
	n.wg.Add(1)
	n.mu.Unlock()
	defer n.wg.Done()
	defer func() { n.mu.Lock(); n.active--; budget.active--; budget.last = time.Now(); n.mu.Unlock() }()
	defer s.Close()
	stop := context.AfterFunc(n.ctx, func() { s.Reset() })
	defer stop()
	s.SetDeadline(time.Now().Add(10 * time.Second))
	if e := s.Scope().ReserveMemory(wire.MaxFrame, network.ReservationPriorityAlways); e != nil {
		s.Reset()
		return
	}
	defer s.Scope().ReleaseMemory(wire.MaxFrame)
	var m messaging.Message
	if e := wire.Read(s, &m); e != nil {
		s.Reset()
		return
	}
	reply := messaging.Reply{Version: 1, ID: m.ID, Type: "error"}
	if !messaging.ValidID(m.ID) {
		reply.ID = ""
	}
	if e := m.Validate(); e != nil {
		reply.Code = e.Error()
		wire.Write(s, reply)
		return
	}
	// Exact duplicates do not consume the new-message rate allowance.
	status, e := n.Journal.ReceiveLimited(p, m, budget.limiter.Allow)
	if e != nil {
		reply.Code = "STORAGE_ERROR"
		if e.Error() == "MESSAGE_ID_CONFLICT" || e.Error() == "RATE_LIMITED" {
			reply.Code = e.Error()
		}
		n.setError(reply.Code)
	} else {
		reply.Type = "ack"
		reply.Status = status
		if status == "stored" && n.onStored != nil {
			n.onStored(p, m)
		}
	}
	s.SetWriteDeadline(time.Now().Add(config.Duration(n.Config.WriteTimeout)))
	if e = wire.Write(s, reply); e != nil {
		s.Reset()
	}
}
func (n *Node) Send(ctx context.Context, p peer.ID, m messaging.Message, retry bool) (Delivery, error) {
	ctx, cancel := context.WithTimeout(ctx, config.Duration(n.Config.SendTimeout))
	defer cancel()
	result := Delivery{ID: m.ID, Peer: p.String(), Status: "not_sent"}
	select {
	case n.sendGate <- struct{}{}:
		defer func() { <-n.sendGate }()
	case <-ctx.Done():
		result.Code = "RESOURCE_LIMIT"
		return result, nil
	}
	if retry {
		saved, e := n.Journal.Outgoing(m.ID)
		if e != nil {
			return result, e
		}
		m = *saved.Message
		p, e = peer.Decode(saved.Peer)
		if e != nil {
			return result, e
		}
		result.Peer = saved.Peer
		if saved.Status == "delivered" {
			return Delivery{ID: m.ID, Peer: saved.Peer, Status: saved.Status, Route: saved.Route, Code: saved.Code}, nil
		}
		if e = n.Journal.Status(p, m.ID, "pending", "", ""); e != nil {
			return result, e
		}
	} else {
		if e := m.Validate(); e != nil {
			return result, e
		}
		if e := n.Journal.Pending(p, m); e != nil {
			return result, e
		}
	}
	finish := func() (Delivery, error) {
		e := n.Journal.Status(p, m.ID, result.Status, result.Code, result.Route)
		if e != nil {
			result.Code = "STORAGE_ERROR"
			return result, e
		}
		return result, nil
	}
	if ctx.Err() != nil {
		result.Code = "CONNECT_TIMEOUT"
		return finish()
	}
	s, route, e := n.openStream(ctx, p)
	result.Route = route
	if e != nil {
		result.Code = e.Error()
		return finish()
	}
	defer s.Close()
	stop := context.AfterFunc(ctx, func() { s.Reset() })
	defer stop()
	if s.Conn().RemotePeer() != p {
		result.Code = "PROTOCOL_ERROR"
		s.Reset()
		return finish()
	}
	s.SetWriteDeadline(deadline(ctx, config.Duration(n.Config.WriteTimeout)))
	result.Status = "delivery_unknown" // conservative from the first write, even partial
	if e = wire.Write(s, m); e != nil {
		result.Code = "WRITE_ERROR"
		s.Reset()
		return finish()
	}
	s.SetReadDeadline(deadline(ctx, config.Duration(n.Config.AckTimeout)))
	var reply messaging.Reply
	if e = wire.Read(s, &reply); e != nil {
		result.Code = "ACK_TIMEOUT"
		s.Reset()
		return finish()
	}
	if e = reply.Validate(m.ID); e != nil {
		result.Code = "PROTOCOL_ERROR"
		return finish()
	}
	if reply.Type == "error" {
		result.Status = "rejected"
		result.Code = reply.Code
	} else {
		result.Status = "delivered"
		result.ACK = reply.Status
	}
	return finish()
}
func (n *Node) Retry(ctx context.Context, id string) (Delivery, error) {
	return n.Send(ctx, "", messaging.Message{ID: id}, true)
}
func (n *Node) openStream(ctx context.Context, p peer.ID) (network.Stream, string, error) {
	return n.openProtocol(ctx, p, messaging.Protocol)
}
func (n *Node) openProtocol(ctx context.Context, p peer.ID, proto protocol.ID) (network.Stream, string, error) {
	if n.Config.SmartRouting && proto != routeProtocol {
		return n.smartStream(ctx, p, proto)
	}
	return n.baseStream(ctx, p, proto)
}
func (n *Node) baseStream(ctx context.Context, p peer.ID, proto protocol.ID) (network.Stream, string, error) {
	limited := network.WithAllowLimitedConn(ctx, "p2pm-chat")
	if len(n.Host.Network().ConnsToPeer(p)) > 0 {
		attempt, cancel := context.WithTimeout(limited, config.Duration(n.Config.RelayTimeout))
		s, e := n.Host.NewStream(attempt, p, proto)
		cancel()
		if e == nil {
			return s, streamRoute(s), nil
		}
	}
	f, e := n.Find(ctx, p, false)
	if e != nil {
		return nil, "", e
	}
	var direct, relayed []ma.Multiaddr
	for _, str := range f.Addresses {
		a, e := ma.NewMultiaddr(str)
		if e != nil {
			continue
		}
		if strings.Contains(str, "/p2p-circuit") {
			relayed = append(relayed, a)
		} else {
			direct = append(direct, a)
		}
	}
	if len(direct) == 0 && len(relayed) == 0 {
		return nil, "", errors.New("NO_USABLE_ADDRESS")
	}
	for _, stage := range []struct {
		addrs    []ma.Multiaddr
		duration time.Duration
		route    string
	}{{direct, config.Duration(n.Config.DirectTimeout), "direct"}, {relayed, config.Duration(n.Config.RelayTimeout), "relay"}} {
		if len(stage.addrs) == 0 {
			continue
		}
		attempt, cancel := context.WithTimeout(limited, stage.duration)
		n.Host.Peerstore().ClearAddrs(p)
		e = n.Host.Connect(attempt, peer.AddrInfo{ID: p, Addrs: stage.addrs})
		if e == nil {
			var s network.Stream
			s, e = n.Host.NewStream(attempt, p, proto)
			if e == nil {
				cancel()
				return s, streamRoute(s), nil
			}
		}
		cancel()
		if ctx.Err() != nil {
			break
		}
	}
	return nil, "", errors.New("CONNECT_TIMEOUT")
}
func streamRoute(s network.Stream) string {
	if _, e := s.Conn().RemoteMultiaddr().ValueForProtocol(ma.P_CIRCUIT); e == nil {
		return "relay"
	}
	if sameLAN(s.Conn().RemoteMultiaddr()) {
		return "lan"
	}
	return "direct"
}
