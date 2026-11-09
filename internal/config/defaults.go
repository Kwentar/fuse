package config

import (
	_ "embed"
	"encoding/json"
)

//go:embed default-network.json
var defaultNetwork []byte

func WithDefaultNetwork(c Client) Client {
	var network struct {
		Bootstrap []string `json:"bootstrap_nodes"`
		Relays    []string `json:"relay_nodes"`
	}
	if json.Unmarshal(defaultNetwork, &network) == nil {
		c.Bootstrap = network.Bootstrap
		c.Relays = network.Relays
	}
	return c
}
