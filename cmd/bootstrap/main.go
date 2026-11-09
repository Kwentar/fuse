package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"fuse/internal/config"
	netstack "fuse/internal/network"
	"fuse/internal/server"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	path := flag.String("config", "configs/bootstrap.json", "server JSON configuration")
	flag.Parse()
	c, e := config.LoadServer(*path)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(6)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	s, e := server.Open(ctx, c)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(6)
	}
	defer s.Close()
	addresses := []string{}
	for _, a := range s.Host.Addrs() {
		addresses = append(addresses, a.String()+"/p2p/"+s.Host.ID().String())
	}
	b, _ := json.Marshal(map[string]any{"peer_id": s.Host.ID().String(), "addresses": addresses, "dht_prefix": "/p2pm", "max_record_age": "36h", "relay": c.Relay, "max_connections": 256, "max_streams": 1024, "resource_limits": netstack.EffectiveLimits(true).ToPartialLimitConfig()})
	fmt.Println(string(b))
	<-ctx.Done()
}
