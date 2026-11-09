package network

import (
	ma "github.com/multiformats/go-multiaddr"
	"net"
	"testing"
)

func TestForbiddenResolvedDestinations(t *testing.T) {
	g := AddressGate{}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.1.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "255.255.255.255", "224.0.0.1", "100.64.0.1", "::1", "fe80::1", "fc00::1"} {
		if PublicIP(net.ParseIP(ip)) {
			t.Error("nonpublic IP allowed", ip)
		}
		family := "ip4"
		if net.ParseIP(ip).To4() == nil {
			family = "ip6"
		}
		if g.InterceptAddrDial("", ma.StringCast("/"+family+"/"+ip+"/tcp/80")) {
			t.Error("resolved dial allowed", ip)
		}
	}
	if !g.InterceptAddrDial("", ma.StringCast("/ip4/8.8.8.8/tcp/4001")) {
		t.Fatal("public address blocked")
	}
	if g.InterceptAddrDial("", ma.StringCast("/dns4/localhost/tcp/80")) {
		t.Fatal("unresolved name bypassed gate")
	}
}
