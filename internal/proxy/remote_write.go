package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/api"
	"github.com/argy/otel-traffic-regulator/internal/filter"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

type RemoteWriteForwarder struct {
	upstream *url.URL
	client   *http.Client
	state    *api.State
}

func NewRemoteWriteForwarder(upstream *url.URL, state *api.State) *RemoteWriteForwarder {
	return &RemoteWriteForwarder{
		upstream: upstream,
		client:   &http.Client{Timeout: 30 * time.Second},
		state:    state,
	}
}

func (f *RemoteWriteForwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.state.Counters.Requests.Add(1)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		http.Error(w, "request body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	decoded, err := snappy.Decode(nil, body)
	if err != nil {
		http.Error(w, "invalid snappy remote-write body", http.StatusBadRequest)
		return
	}
	var request prompb.WriteRequest
	if err := request.Unmarshal(decoded); err != nil {
		http.Error(w, "invalid remote-write protobuf", http.StatusBadRequest)
		return
	}

	now := time.Now()
	filtered := make([]prompb.TimeSeries, 0, len(request.Timeseries))
	for _, ts := range request.Timeseries {
		key := filter.CanonicalSeries(ts.Labels)
		sampleCount := uint64(len(ts.Samples))
		f.state.Counters.SamplesReceived.Add(sampleCount)
		f.state.Observe(key, now)
		if f.state.Gate.Allow(key) && f.upstream != nil {
			filtered = append(filtered, ts)
			f.state.Counters.SamplesForwarded.Add(sampleCount)
		} else {
			f.state.Counters.SamplesDropped.Add(sampleCount)
		}
	}
	if len(filtered) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}
	if f.upstream == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	request.Timeseries = filtered
	encoded, err := request.Marshal()
	if err != nil {
		http.Error(w, "failed to encode remote-write protobuf", http.StatusInternalServerError)
		return
	}
	forwarded := snappy.Encode(nil, encoded)
	upstreamRequest, err := http.NewRequestWithContext(r.Context(), http.MethodPost, f.upstream.String(), bytes.NewReader(forwarded))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusInternalServerError)
		return
	}
	upstreamRequest.Header.Set("Content-Type", "application/x-protobuf")
	upstreamRequest.Header.Set("Content-Encoding", "snappy")
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

func copyHeader(dst, src http.Header, names ...string) {
	for _, name := range names {
		for _, value := range src.Values(name) {
			dst.Add(name, value)
		}
	}
}
