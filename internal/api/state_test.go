package api

import (
	"testing"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/series"
)

func TestPolicyMergeUsesVersionThenOrigin(t *testing.T) {
	state := NewState(series.NewStore(time.Minute, 1), "node-a")
	defer state.Store.Close()
	if !state.MergePolicy(Policy{AllowedPercentage: 20, Version: 2, Origin: "node-b"}) {
		t.Fatal("expected newer policy to merge")
	}
	if state.MergePolicy(Policy{AllowedPercentage: 80, Version: 1, Origin: "node-c"}) {
		t.Fatal("older policy merged")
	}
	if got := state.CurrentPolicy().AllowedPercentage; got != 20 {
		t.Fatalf("allowed percentage = %d, want 20", got)
	}
}
