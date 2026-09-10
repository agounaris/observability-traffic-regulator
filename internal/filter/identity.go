package filter

import (
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/prometheus/prometheus/prompb"
)

// CanonicalSeries returns a stable identity for a Prometheus time series.
func CanonicalSeries(labels []prompb.Label) string {
	copyLabels := append([]prompb.Label(nil), labels...)
	sort.Slice(copyLabels, func(i, j int) bool { return copyLabels[i].Name < copyLabels[j].Name })
	var b strings.Builder
	for _, label := range copyLabels {
		b.WriteString(strconv.Quote(label.Name))
		b.WriteByte('=')
		b.WriteString(strconv.Quote(label.Value))
		b.WriteByte(',')
	}
	return b.String()
}

func Hash(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}

type Gate struct {
	allowed atomic.Uint32
}

func NewGate() *Gate                { g := &Gate{}; g.allowed.Store(100); return g }
func (g *Gate) SetAllowed(v uint32) { g.allowed.Store(v) }
func (g *Gate) Allowed() uint32     { return g.allowed.Load() }
func (g *Gate) Allow(key string) bool {
	allowed := g.allowed.Load()
	return allowed == 100 || (allowed != 0 && Hash(key)%100 < allowed)
}
