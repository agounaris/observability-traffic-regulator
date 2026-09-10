package api

import (
	"fmt"
	"net/http"
)

func writeMetrics(w http.ResponseWriter, state *State) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP regulator_allowed_percentage Current percentage of series admitted.\n# TYPE regulator_allowed_percentage gauge\nregulator_allowed_percentage %d\n", state.CurrentPolicy().AllowedPercentage)
	fmt.Fprintf(w, "# HELP regulator_active_series Series observed within the expiry window.\n# TYPE regulator_active_series gauge\nregulator_active_series %d\n", state.Store.Active())
	writeCounter(w, "regulator_received_requests_total", "Requests received by the regulator.", state.Counters.Requests.Load())
	writeCounter(w, "regulator_received_samples_total", "Samples received by the regulator.", state.Counters.SamplesReceived.Load())
	writeCounter(w, "regulator_forwarded_samples_total", "Samples forwarded upstream.", state.Counters.SamplesForwarded.Load())
	writeCounter(w, "regulator_dropped_samples_total", "Samples dropped by the traffic policy.", state.Counters.SamplesDropped.Load())
	writeCounter(w, "regulator_upstream_errors_total", "Upstream forwarding errors.", state.Counters.UpstreamErrors.Load())
}

func writeCounter(w http.ResponseWriter, name, help string, value uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, value)
}
