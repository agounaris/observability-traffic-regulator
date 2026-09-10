package gossip

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/api"
)

// Node implements small fixed-peer anti-entropy gossip. It intentionally does
// not do membership discovery: deployments provide the peer URLs explicitly.
type Node struct {
	state    *api.State
	peers    []string
	secret   string
	interval time.Duration
	client   *http.Client
}

func New(state *api.State, peers []string, secret string, interval time.Duration) *Node {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	clean := make([]string, 0, len(peers))
	for _, peer := range peers {
		peer = strings.TrimRight(strings.TrimSpace(peer), "/")
		if peer != "" {
			clean = append(clean, peer)
		}
	}
	return &Node{state: state, peers: clean, secret: secret, interval: interval, client: &http.Client{Timeout: interval}}
}

func (n *Node) Start(ctx context.Context) {
	if len(n.peers) == 0 {
		return
	}
	go func() {
		n.syncAll(ctx)
		ticker := time.NewTicker(n.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n.syncAll(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (n *Node) syncAll(ctx context.Context) {
	for _, peer := range n.peers {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, peer+"/internal/gossip/state", nil)
		if err != nil {
			continue
		}
		if n.secret != "" {
			request.Header.Set("X-Regulator-Gossip-Secret", n.secret)
		}
		response, err := n.client.Do(request)
		if err != nil {
			continue
		}
		var policy api.Policy
		if response.StatusCode == http.StatusOK {
			_ = json.NewDecoder(response.Body).Decode(&policy)
		}
		response.Body.Close()
		if response.StatusCode == http.StatusOK {
			n.state.MergePolicy(policy)
		}
	}
}

func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	if n.secret != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Regulator-Gossip-Secret")), []byte(n.secret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(n.state.CurrentPolicy())
}
