package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/filter"
	"github.com/prometheus/prometheus/prompb"
)

type Forwarder interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}

func NewHandler(state *State, forwarder Forwarder, otlpForwarder Forwarder, gossipHandler http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) { writeMetrics(w, state) })
	mux.HandleFunc("/api/set", setHandler(state))
	mux.HandleFunc("/api/status", statusHandler(state))
	mux.HandleFunc("/api/exists", existsHandler(state))
	mux.Handle("/api/remote_write", forwarder)
	mux.Handle("/api/v1/write", forwarder)
	mux.Handle("/v1/metrics", otlpForwarder)
	if gossipHandler != nil {
		mux.Handle("/internal/gossip/state", gossipHandler)
	}
	mux.Handle("/", forwarder)
	return mux
}

func setHandler(state *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			state.Logger.Warn("audit policy change rejected", "audit", true, "action", "set_allowed_percentage", "reason", "method_not_allowed", "remote_addr", r.RemoteAddr, "user_agent", r.UserAgent())
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		v, err := strconv.Atoi(r.URL.Query().Get("allowed_percentage"))
		if err != nil || v < 0 || v > 100 {
			state.Logger.Warn("audit policy change rejected", "audit", true, "action", "set_allowed_percentage", "reason", "invalid_percentage", "remote_addr", r.RemoteAddr, "user_agent", r.UserAgent())
			http.Error(w, "allowed_percentage must be an integer from 0 to 100", http.StatusBadRequest)
			return
		}
		previous := state.CurrentPolicy()
		policy := state.SetLocalAllowed(uint32(v))
		state.Logger.Info("audit policy changed",
			"audit", true,
			"action", "set_allowed_percentage",
			"previous_allowed_percentage", previous.AllowedPercentage,
			"allowed_percentage", policy.AllowedPercentage,
			"policy_version", policy.Version,
			"policy_origin", policy.Origin,
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
		)
		writeJSON(w, policy)
	}
}

func statusHandler(state *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		policy := state.CurrentPolicy()
		writeJSON(w, map[string]any{"allowed_percentage": policy.AllowedPercentage, "policy_version": policy.Version, "policy_origin": policy.Origin, "policy_updated_at": policy.UpdatedAt, "active_series": state.Store.Active()})
	}
}

func existsHandler(state *State) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET required", http.StatusMethodNotAllowed)
			return
		}
		key, err := parseSample(r.URL.Query().Get("sample"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]any{"exists": state.Store.Exists(key, time.Now())})
	}
}

func parseSample(value string) (string, error) {
	value = strings.TrimSpace(value)
	open := strings.IndexByte(value, '{')
	if open < 0 {
		if value == "" {
			return "", fmt.Errorf("sample is required")
		}
		return strconv.Quote(value) + ",", nil
	}
	if !strings.HasSuffix(value, "}") {
		return "", fmt.Errorf("invalid sample selector")
	}
	metric := strings.TrimSpace(value[:open])
	if metric == "" {
		return "", fmt.Errorf("metric name is required")
	}
	body := strings.TrimSpace(value[open+1 : len(value)-1])
	labels := []promLabel{}
	for _, part := range splitLabels(body) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		pieces := strings.SplitN(part, "=", 2)
		if len(pieces) != 2 {
			return "", fmt.Errorf("invalid label %q", part)
		}
		name := strings.TrimSpace(pieces[0])
		val := strings.Trim(strings.TrimSpace(pieces[1]), "\"")
		labels = append(labels, promLabel{name, val})
	}
	return filter.CanonicalSeries(toPromLabels(metric, labels)), nil
}

type promLabel struct{ name, value string }

func toPromLabels(metric string, labels []promLabel) []prompb.Label {
	out := make([]prompb.Label, 0, len(labels)+1)
	out = append(out, prompb.Label{Name: "__name__", Value: metric})
	for _, l := range labels {
		out = append(out, prompb.Label{Name: l.name, Value: l.value})
	}
	return out
}
func splitLabels(s string) []string {
	var out []string
	start := 0
	quoted := false
	for i, r := range s {
		if r == '"' {
			quoted = !quoted
		}
		if r == ',' && !quoted {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
