package network

import (
	"net"

	"github.com/libp2p/go-libp2p/core/control"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

func PublicIP(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!ip.IsUnspecified() && !net.IPv4(255, 255, 255, 255).Equal(ip) && !(ip.To4() != nil && ip.To4()[0] == 100 && ip.To4()[1]&192 == 64)
}

// The swarm calls InterceptAddrDial AFTER DNS resolution. Unresolved names are
// rejected here; the actual transport dials precisely the checked IP.
type AddressGate struct {
	AllowPrivate bool
	AllowLAN     func(peer.ID, ma.Multiaddr) bool
}

func (g AddressGate) InterceptPeerDial(peer.ID) bool { return true }
func (g AddressGate) InterceptAddrDial(p peer.ID, a ma.Multiaddr) bool {
	if g.AllowPrivate {
		return true
	}
	if g.AllowLAN != nil && g.AllowLAN(p, a) {
		return true
	}
	for _, code := range []int{ma.P_IP4, ma.P_IP6} {
		if value, e := a.ValueForProtocol(code); e == nil {
			return PublicIP(net.ParseIP(value))
		}
	}
	// A bare circuit address uses an already authenticated relay connection.
	_, e := a.ValueForProtocol(ma.P_CIRCUIT)
	return e == nil
}
func (g AddressGate) InterceptAccept(network.ConnMultiaddrs) bool { return true }
func (g AddressGate) InterceptSecured(network.Direction, peer.ID, network.ConnMultiaddrs) bool {
	return true
}
func (g AddressGate) InterceptUpgraded(network.Conn) (bool, control.DisconnectReason) { return true, 0 }
