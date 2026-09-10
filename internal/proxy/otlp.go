package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/api"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	collector "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type OTLPForwarder struct {
	upstream *url.URL
	client   *http.Client
	state    *api.State
}

func NewOTLPForwarder(upstream *url.URL, state *api.State) *OTLPForwarder {
	return &OTLPForwarder{upstream: upstream, client: &http.Client{Timeout: 30 * time.Second}, state: state}
}

func (f *OTLPForwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	body, err := readOTLPBody(w, r)
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	var request collector.MetricsData
	isJSON := strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "json")
	if isJSON {
		if err := protojson.Unmarshal(body, &request); err != nil {
			http.Error(w, "invalid OTLP metrics JSON", http.StatusBadRequest)
			return
		}
	} else if err := proto.Unmarshal(body, &request); err != nil {
		http.Error(w, "invalid OTLP metrics protobuf", http.StatusBadRequest)
		return
	}

	now := time.Now()
	discard := f.upstream == nil
	kept := uint64(0)
	dropped := uint64(0)
	for _, resourceMetrics := range request.ResourceMetrics {
		resourceKey := attributesKey(resourceMetrics.GetResource().GetAttributes())
		for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
			scopeKey := ""
			if scope := scopeMetrics.GetScope(); scope != nil {
				scopeKey = scope.GetName() + "@" + scope.GetVersion()
			}
			for _, metric := range scopeMetrics.Metrics {
				base := resourceKey + "|" + scopeKey + "|" + metric.GetName() + "|" + metric.GetUnit()
				k, d := filterOTLPMetric(metric, base, f.state, now, discard)
				kept += k
				dropped += d
			}
		}
	}
	f.state.Counters.SamplesReceived.Add(kept + dropped)
	f.state.Counters.SamplesForwarded.Add(kept)
	f.state.Counters.SamplesDropped.Add(dropped)
	f.state.Counters.Requests.Add(1)
	if kept == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	var encoded []byte
	if isJSON {
		encoded, err = protojson.Marshal(&request)
	} else {
		encoded, err = proto.Marshal(&request)
	}
	if err != nil {
		http.Error(w, "failed to encode OTLP metrics protobuf", http.StatusInternalServerError)
		return
	}
	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, f.upstream.String(), bytes.NewReader(encoded))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/x-protobuf"
	}
	upstreamRequest.Header.Set("Content-Type", contentType)
	if r.Header.Get("Content-Encoding") == "gzip" {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(encoded); err != nil || writer.Close() != nil {
			http.Error(w, "failed to gzip OTLP metrics", http.StatusInternalServerError)
			return
		}
		upstreamRequest.Body = io.NopCloser(bytes.NewReader(compressed.Bytes()))
		upstreamRequest.ContentLength = int64(compressed.Len())
		upstreamRequest.Header.Set("Content-Encoding", "gzip")
	}
	upstreamRequest.Header.Set("User-Agent", "otel-traffic-regulator")
	copyHeader(upstreamRequest.Header, r.Header, "Authorization", "X-Scope-OrgID")
	response, err := f.client.Do(upstreamRequest)
	if err != nil {
		f.state.Counters.UpstreamErrors.Add(1)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		w.Header()[key] = values
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func readOTLPBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	reader := io.Reader(http.MaxBytesReader(w, r.Body, 64<<20))
	if r.Header.Get("Content-Encoding") == "gzip" {
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		reader = io.LimitReader(gzipReader, 64<<20+1)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if len(body) > 64<<20 {
		return nil, io.ErrShortBuffer
	}
	return body, nil
}

func filterOTLPMetric(metric *collector.Metric, base string, state *api.State, now time.Time, discard bool) (uint64, uint64) {
	var kept, dropped uint64
	allow := func(attrs []*commonpb.KeyValue) bool {
		key := base + "|" + attributesKey(attrs)
		state.Observe(key, now)
		return state.Gate.Allow(key) && !discard
	}
	if gauge := metric.GetGauge(); gauge != nil {
		out := gauge.DataPoints[:0]
		for _, point := range gauge.DataPoints {
			if allow(point.Attributes) {
				out = append(out, point)
				kept++
			} else {
				dropped++
			}
		}
		gauge.DataPoints = out
	}
	if sum := metric.GetSum(); sum != nil {
		out := sum.DataPoints[:0]
		for _, point := range sum.DataPoints {
			if allow(point.Attributes) {
				out = append(out, point)
				kept++
			} else {
				dropped++
			}
		}
		sum.DataPoints = out
	}
	if histogram := metric.GetHistogram(); histogram != nil {
		out := histogram.DataPoints[:0]
		for _, point := range histogram.DataPoints {
			if allow(point.Attributes) {
				out = append(out, point)
				kept++
			} else {
				dropped++
			}
		}
		histogram.DataPoints = out
	}
	if histogram := metric.GetExponentialHistogram(); histogram != nil {
		out := histogram.DataPoints[:0]
		for _, point := range histogram.DataPoints {
			if allow(point.Attributes) {
				out = append(out, point)
				kept++
			} else {
				dropped++
			}
		}
		histogram.DataPoints = out
	}
	if summary := metric.GetSummary(); summary != nil {
		out := summary.DataPoints[:0]
		for _, point := range summary.DataPoints {
			if allow(point.Attributes) {
				out = append(out, point)
				kept++
			} else {
				dropped++
			}
		}
		summary.DataPoints = out
	}
	return kept, dropped
}

func attributesKey(attributes []*commonpb.KeyValue) string {
	keys := make([]string, 0, len(attributes))
	for _, attribute := range attributes {
		if attribute == nil {
			continue
		}
		encoded, _ := proto.Marshal(attribute.GetValue())
		keys = append(keys, attribute.GetKey()+"="+hex.EncodeToString(encoded))
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
