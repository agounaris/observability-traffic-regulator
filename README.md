# OTEL Traffic Regulator

Small Go HTTP traffic regulator for Prometheus remote write and OTLP metrics.
It sits between a collector such as Grafana Alloy and a Mimir, Thanos Receive,
or OTLP-compatible metrics backend.

## Run

```sh
go run ./cmd/regulator -listen :8080 \
  -upstream https://mimir.example.com/api/v1/push
```

For a separate OTLP/HTTP metrics backend, configure both upstreams:

```sh
go run ./cmd/regulator -listen :8080 \
  -upstream https://mimir.example.com/api/v1/push \
  -otlp-upstream https://otel-gateway.example.com/v1/metrics
```

If `-otlp-upstream` is omitted, it defaults to `-upstream`.

If `-upstream` is omitted, the regulator runs in discard mode. It accepts and
counts incoming traffic, maintains the active-series index, and returns success
to collectors without forwarding any samples.

The regulator starts with 100% of series admitted. Change the policy at
runtime:

```sh
curl -XPOST 'http://localhost:8080/api/set?allowed_percentage=80'
curl http://localhost:8080/api/status
```

Each `/api/set` request produces a structured audit log entry containing the
previous value, new value, policy version, origin node, caller address, and
user agent. Invalid policy-change attempts are logged as rejected audit events.

Filtering is deterministic by canonical series identity, so the same series is
consistently admitted or rejected. Intentionally rejected samples receive a
successful response from the regulator and are not retried by the collector.
Upstream failures are returned as gateway errors so the collector can retry.

The accepted remote-write paths are `/`, `/api/remote_write`, and
`/api/v1/write`. OTLP/HTTP metrics are accepted at `/v1/metrics` using
protobuf or JSON, with optional gzip compression. The regulator also exposes
`/metrics`, `/healthz`, `/readyz`, and `/api/status`.

Query whether a series has been observed in the last ten minutes:

```sh
curl --get 'http://localhost:8080/api/exists' \
  --data-urlencode 'sample=a_metric_count{label1="test",label2="test"}'
```

The in-memory series index is exact, sharded, and expires entries after ten
minutes without an update. It is local to one process. Policy synchronization
between replicas is available through fixed-peer gossip; see the Kubernetes
example below.

## Choosing a protocol

For Prometheus-style metrics, remote write is normally the higher-throughput
option: it uses compact protobuf/Snappy batches and has lower proxy processing
cost. OTLP carries richer resource, scope, metric-type, and datapoint
semantics, but generally uses more CPU and bandwidth. Use remote write for
Alloy/Prometheus-to-Mimir or Thanos paths, and OTLP when the source and
destination are already OTLP-native.

The actual ratio depends on batch size, label cardinality, compression, and the
upstream. Benchmark with representative traffic before capacity planning.

## Current scope

The implementation currently handles:

- Prometheus remote-write protobuf with Snappy compression
- OTLP/HTTP metrics with protobuf or JSON payloads
- OTLP/HTTP gzip payloads
- deterministic series-level admission filtering
- in-memory active-series tracking with ten-minute expiry

OTLP/gRPC is not currently implemented. Authentication for the control API
should be added before exposing it outside a trusted network.

See [`examples/`](examples/) for a Docker Compose stack using Grafana Alloy,
the regulator, and Prometheus.

For multiple regulator replicas, provide a stable node ID, peer URLs, and a
shared gossip secret to every instance. The `/api/set` call can then target any
replica and the policy converges across peers:

```sh
./regulator -node-id regulator-0 \
  -gossip-peers http://regulator-1:8080,http://regulator-2:8080 \
  -gossip-secret "$REGULATOR_GOSSIP_SECRET"
```

This is fixed-peer anti-entropy gossip, not membership discovery. Protect the
gossip endpoint with network policy or mTLS in production; the shared secret is
intended as a simple first layer for a trusted network. Policy is currently
kept in memory, so persist it or use a durable control store if all replicas
may restart simultaneously.

For a complete three-replica StatefulSet, headless Service, gossip Secret, and
load-balanced client Service, see [`examples/k8s/`](examples/k8s/).
