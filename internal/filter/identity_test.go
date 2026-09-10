package filter

import (
	"testing"

	"github.com/prometheus/prometheus/prompb"
)

func TestCanonicalSeriesIsLabelOrderIndependent(t *testing.T) {
	a := CanonicalSeries([]prompb.Label{{Name: "__name__", Value: "requests_total"}, {Name: "status", Value: "200"}, {Name: "method", Value: "GET"}})
	b := CanonicalSeries([]prompb.Label{{Name: "method", Value: "GET"}, {Name: "status", Value: "200"}, {Name: "__name__", Value: "requests_total"}})
	if a != b {
		t.Fatalf("series identity differs: %q != %q", a, b)
	}
}

func TestGateBoundaries(t *testing.T) {
	g := NewGate()
	if !g.Allow("anything") {
		t.Fatal("100 percent gate rejected a series")
	}
	g.SetAllowed(0)
	if g.Allow("anything") {
		t.Fatal("0 percent gate admitted a series")
	}
}
