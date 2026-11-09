package server

import (
	"context"
	"log"
	"time"

	"fuse/internal/config"
	"fuse/internal/discovery"
	"fuse/internal/identity"
	netstack "fuse/internal/network"
	"fuse/internal/securefs"
	libp2p "github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/relay"
	ma "github.com/multiformats/go-multiaddr"
)

type Server struct {
	Host   host.Host
	DHT    *dht.IpfsDHT
	Relay  *relay.Relay
	lock   *securefs.Lock
	cancel context.CancelFunc
	done   chan struct{}
}

func Open(parent context.Context, c config.Server) (result *Server, err error) {
	if err = c.Validate(); err != nil {
		return nil, err
	}
	if err = securefs.EnsureDir(c.DataDir); err != nil {
		return nil, err
	}
	lock, err := securefs.Acquire(c.DataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{lock: lock}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if _, err = identity.Init(c.DataDir); err != nil {
		return nil, err
	}
	key, err := identity.Load(c.DataDir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	var opts []libp2p.Option
	if len(c.Announce) > 0 {
		addrs := []ma.Multiaddr{}
		for _, a := range c.Announce {
			addrs = append(addrs, ma.StringCast(a))
		}
		opts = append(opts, libp2p.AddrsFactory(func(_ []ma.Multiaddr) []ma.Multiaddr { return addrs }))
	}
	if s.Host, err = netstack.NewHost(key, c.Listen, nil, true, c.AllowPrivate, opts...); err != nil {
		return nil, err
	}
	bootstrap, err := config.Peers(c.Bootstrap)
	if err != nil {
		return nil, err
	}
	if s.DHT, err = discovery.NewDHT(ctx, s.Host, true, discovery.Validator{AllowPrivate: c.AllowPrivate, Conflict: func() { log.Print("CONTACT_SEQ_CONFLICT") }}, bootstrap); err != nil {
		return nil, err
	}
	r := c.Relay
	resources := relay.Resources{ReservationTTL: config.Duration(r.ReservationTTL), MaxReservations: r.Reservations, MaxCircuits: r.Circuits, BufferSize: 2048, MaxReservationsPerPeer: r.PerPeer, MaxReservationsPerIP: r.PerIP, MaxReservationsPerASN: r.PerASN, Limit: &relay.RelayLimit{Duration: config.Duration(r.Duration), Data: r.Data}}
	if s.Relay, err = relay.New(s.Host, relay.WithResources(resources)); err != nil {
		return nil, err
	}
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		for {
			for _, p := range bootstrap {
				attempt, stop := context.WithTimeout(ctx, 6*time.Second)
				s.Host.Connect(attempt, p)
				stop()
			}
			s.DHT.Bootstrap(ctx)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
	}()
	return s, nil
}
func (s *Server) Close() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
	if s.Relay != nil {
		s.Relay.Close()
	}
	if s.DHT != nil {
		s.DHT.Close()
	}
	if s.Host != nil {
		s.Host.Close()
	}
	if s.lock != nil {
		s.lock.Close()
	}
}
