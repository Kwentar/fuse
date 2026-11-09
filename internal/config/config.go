package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"fuse/internal/wire"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
)

type Client struct {
	Version          int      `json:"config_version"`
	Bootstrap        []string `json:"bootstrap_nodes"`
	Relays           []string `json:"relay_nodes"`
	Direct           []string `json:"direct_listen_addrs"`
	AllowPrivate     bool     `json:"allow_private_addresses"`
	SendTimeout      string   `json:"send_timeout"`
	DiscoveryTimeout string   `json:"discovery_timeout"`
	DirectTimeout    string   `json:"direct_dial_timeout"`
	RelayTimeout     string   `json:"relay_dial_timeout"`
	WriteTimeout     string   `json:"write_timeout"`
	AckTimeout       string   `json:"ack_timeout"`
	PublishInterval  string   `json:"publish_interval"`
	LogLevel         string   `json:"log_level"`
	SmartRouting     bool     `json:"smart_routing"`
}

func DefaultClient() Client {
	return Client{1, []string{}, []string{}, []string{}, false, "30s", "6s", "2s", "10s", "5s", "5s", "5m", "info", false}
}
func Duration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }
func (c Client) Validate() error {
	if c.Version != 1 {
		return errors.New("unsupported config_version")
	}
	for name, value := range map[string]string{"send_timeout": c.SendTimeout, "discovery_timeout": c.DiscoveryTimeout, "direct_dial_timeout": c.DirectTimeout, "relay_dial_timeout": c.RelayTimeout, "write_timeout": c.WriteTimeout, "ack_timeout": c.AckTimeout, "publish_interval": c.PublishInterval} {
		d, e := time.ParseDuration(value)
		if e != nil || d <= 0 {
			return fmt.Errorf("invalid %s: use a positive Go duration", name)
		}
	}
	if Duration(c.SendTimeout) > 30*time.Second {
		return errors.New("send_timeout must not exceed 30s")
	}
	if c.LogLevel != "info" && c.LogLevel != "debug" && c.LogLevel != "error" {
		return errors.New("invalid log_level")
	}
	if _, e := Peers(c.Bootstrap); e != nil {
		return fmt.Errorf("bootstrap_nodes: %w", e)
	}
	if _, e := Peers(c.Relays); e != nil {
		return fmt.Errorf("relay_nodes: %w", e)
	}
	for _, s := range c.Direct {
		a, e := ma.NewMultiaddr(s)
		if e != nil {
			return e
		}
		p := a.Protocols()
		if len(p) != 2 || (p[0].Code != ma.P_IP4 && p[0].Code != ma.P_IP6) || p[1].Code != ma.P_TCP {
			return errors.New("direct listener must be IP/TCP")
		}
	}
	return nil
}
func LoadClient(path string) (Client, error) {
	c := DefaultClient()
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if len(b) > 64<<10 {
		return c, errors.New("config too large")
	}
	if e = wire.Decode(b, &c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func Peers(addresses []string) ([]peer.AddrInfo, error) {
	out := make([]peer.AddrInfo, 0, len(addresses))
	for _, s := range addresses {
		a, e := ma.NewMultiaddr(s)
		if e != nil {
			return nil, e
		}
		p, e := peer.AddrInfoFromP2pAddr(a)
		if e != nil {
			return nil, e
		}
		if len(p.Addrs) != 1 {
			return nil, errors.New("missing transport")
		}
		protos := p.Addrs[0].Protocols()
		if len(protos) != 2 || (protos[0].Code != ma.P_IP4 && protos[0].Code != ma.P_IP6 && protos[0].Code != ma.P_DNS4 && protos[0].Code != ma.P_DNS6) || protos[1].Code != ma.P_TCP {
			return nil, errors.New("infrastructure address must be IP or DNS/TCP/p2p")
		}
		out = append(out, *p)
	}
	return out, nil
}

type RelayLimits struct {
	ReservationTTL string `json:"reservation_ttl"`
	Duration       string `json:"circuit_duration"`
	Data           int64  `json:"circuit_bytes_per_direction"`
	Reservations   int    `json:"max_reservations"`
	PerPeer        int    `json:"max_reservations_per_peer"`
	PerIP          int    `json:"max_reservations_per_ip"`
	PerASN         int    `json:"max_reservations_per_asn"`
	Circuits       int    `json:"max_circuits_per_peer"`
}
type Server struct {
	Version      int         `json:"config_version"`
	DataDir      string      `json:"data_dir"`
	Listen       []string    `json:"listen_addrs"`
	Announce     []string    `json:"announce_addrs"`
	Bootstrap    []string    `json:"bootstrap_nodes"`
	AllowPrivate bool        `json:"allow_private_addresses"`
	Prefix       string      `json:"dht_prefix"`
	MaxAge       string      `json:"max_record_age"`
	Relay        RelayLimits `json:"relay"`
	Connections  int         `json:"max_connections"`
	Streams      int         `json:"max_streams"`
}

func DefaultServer() Server {
	return Server{1, "/var/lib/p2pm-bootstrap", []string{"/ip4/0.0.0.0/tcp/4001"}, []string{}, []string{}, false, "/p2pm", "36h", RelayLimits{"1h", "1h", 16 << 20, 64, 1, 32, 64, 4}, 256, 1024}
}
func LoadServer(path string) (Server, error) {
	c := DefaultServer()
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if e = wire.Decode(b, &c); e != nil {
		return c, e
	}
	return c, c.Validate()
}
func (c Server) Validate() error {
	if c.Version != 1 || c.Prefix != "/p2pm" || c.MaxAge != "36h" || c.Connections != 256 || c.Streams != 1024 {
		return errors.New("unsupported server protocol/resource settings")
	}
	if c.DataDir == "" || len(c.Listen) == 0 {
		return errors.New("data_dir and listen_addrs required")
	}
	if _, e := Peers(c.Bootstrap); e != nil {
		return e
	}
	for _, s := range append(append([]string{}, c.Listen...), c.Announce...) {
		a, e := ma.NewMultiaddr(s)
		if e != nil {
			return e
		}
		p := a.Protocols()
		if len(p) != 2 || (p[0].Code != ma.P_IP4 && p[0].Code != ma.P_IP6) || p[1].Code != ma.P_TCP {
			return errors.New("server addresses must be IP/TCP")
		}
	}
	r := c.Relay
	if Duration(r.ReservationTTL) <= 0 || Duration(r.Duration) <= 0 || r.Data <= 0 || r.Reservations <= 0 || r.PerPeer != 1 || r.PerIP <= 0 || r.PerASN <= 0 || r.Circuits <= 0 {
		return errors.New("invalid finite relay limits")
	}
	return nil
}
