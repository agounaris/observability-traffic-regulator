package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/api"
	"github.com/argy/otel-traffic-regulator/internal/gossip"
	"github.com/argy/otel-traffic-regulator/internal/proxy"
	"github.com/argy/otel-traffic-regulator/internal/series"
)

func main() {
	listen := flag.String("listen", ":8080", "HTTP listen address")
	upstream := flag.String("upstream", "", "remote-write upstream URL")
	otlpUpstream := flag.String("otlp-upstream", "", "OTLP/HTTP metrics upstream URL; defaults to -upstream")
	nodeID := flag.String("node-id", "", "stable node ID used for policy conflict resolution")
	peerList := flag.String("gossip-peers", "", "comma-separated regulator peer base URLs")
	gossipSecret := flag.String("gossip-secret", "", "shared secret for internal gossip requests")
	gossipInterval := flag.Duration("gossip-interval", 2*time.Second, "policy gossip interval")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var u *url.URL
	if *upstream != "" {
		parsed, err := url.Parse(*upstream)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			logger.Error("invalid upstream URL", "value", *upstream, "error", err)
			os.Exit(2)
		}
		u = parsed
	} else {
		logger.Warn("no upstream configured; running in discard mode")
	}
	var otlpURL *url.URL = u
	if *otlpUpstream != "" {
		parsed, err := url.Parse(*otlpUpstream)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			logger.Error("invalid OTLP upstream URL", "value", *otlpUpstream, "error", err)
			os.Exit(2)
		}
		otlpURL = parsed
	}

	if *nodeID == "" {
		if hostname, err := os.Hostname(); err == nil {
			*nodeID = hostname
		} else {
			*nodeID = "regulator"
		}
	}
	store := series.NewStore(10*time.Minute, 64)
	defer store.Close()
	state := api.NewStateWithLogger(store, logger, *nodeID)
	peers := make([]string, 0)
	for _, peer := range strings.Split(*peerList, ",") {
		if strings.TrimSpace(peer) != "" {
			peers = append(peers, peer)
		}
	}
	gossipNode := gossip.New(state, peers, *gossipSecret, *gossipInterval)
	gossipNode.Start(context.Background())
	forwarder := proxy.NewRemoteWriteForwarder(u, state)
	otlpForwarder := proxy.NewOTLPForwarder(otlpURL, state)
	handler := api.NewHandler(state, forwarder, otlpForwarder, gossipNode)

	server := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	if u == nil {
		logger.Info("starting regulator", "listen", *listen, "mode", "discard")
	} else {
		logger.Info("starting regulator", "listen", *listen, "upstream", u.String())
	}
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
