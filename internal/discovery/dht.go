package discovery

import (
	"context"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	record "github.com/libp2p/go-libp2p-record"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"time"
)

func NewDHT(ctx context.Context, h host.Host, server bool, v Validator, bootstrap []peer.AddrInfo) (*dht.IpfsDHT, error) {
	mode := dht.ModeClient
	if server {
		mode = dht.ModeServer
	}
	return dht.New(ctx, h, dht.ProtocolPrefix("/p2pm"), dht.Mode(mode), dht.Validator(record.NamespacedValidator{"p2pm": v}), dht.MaxRecordAge(36*time.Hour), dht.BootstrapPeers(bootstrap...), dht.RoutingTableRefreshQueryTimeout(6*time.Second))
}
