package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/argy/otel-traffic-regulator/internal/api"
	"github.com/argy/otel-traffic-regulator/internal/series"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

func TestRemoteWriteForwardingAndFiltering(t *testing.T) {
	var upstreamCalls int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		body, _ := io.ReadAll(r.Body)
		decoded, err := snappy.Decode(nil, body)
		if err != nil {
			t.Errorf("upstream received invalid snappy body: %v", err)
			return
		}
		var request prompb.WriteRequest
		if err := request.Unmarshal(decoded); err != nil {
			t.Errorf("upstream received invalid protobuf: %v", err)
			return
		}
		if len(request.Timeseries) != 1 || len(request.Timeseries[0].Samples) != 1 {
			t.Errorf("unexpected upstream payload: %+v", request)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	store := series.NewStore(10*time.Minute, 2)
	defer store.Close()
	state := api.NewState(store)
	forwarder := NewRemoteWriteForwarder(upstreamURL, state)
	writeRequest := prompb.WriteRequest{Timeseries: []prompb.TimeSeries{{
		Labels:  []prompb.Label{{Name: "__name__", Value: "up"}},
		Samples: []prompb.Sample{{Value: 1, Timestamp: 1}},
	}}}
	body, err := writeRequest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	body = snappy.Encode(nil, body)

	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	response := httptest.NewRecorder()
	forwarder.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("100 percent response = %d", response.Code)
	}
	if upstreamCalls != 1 {
		t.Fatalf("upstream calls = %d, want 1", upstreamCalls)
	}

	state.Gate.SetAllowed(0)
	request = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	response = httptest.NewRecorder()
	forwarder.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("0 percent response = %d", response.Code)
	}
	if upstreamCalls != 1 {
		t.Fatalf("dropped request was forwarded; upstream calls = %d", upstreamCalls)
	}
}
