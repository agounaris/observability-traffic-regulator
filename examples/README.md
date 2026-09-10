# Local example

This stack runs:

```text
Grafana Alloy -> regulator -> Prometheus remote-write receiver
```

Start it from this directory:

```sh
docker compose up --build
```

Alloy exports CPU and memory metrics, scrapes Prometheus' own `/metrics`
endpoint, and sends both sources to the regulator. The regulator forwards them
to Prometheus at `/api/v1/write`.

Useful queries in Prometheus at http://localhost:9090/graph include:

```promql
rate(node_cpu_seconds_total[5m])
node_memory_MemAvailable_bytes
regulator_received_samples_total
```

Change the admission percentage:

```sh
curl -XPOST 'http://localhost:8000/api/set?allowed_percentage=20'
curl http://localhost:8000/metrics
```

Restore normal traffic:

```sh
curl -XPOST 'http://localhost:8000/api/set?allowed_percentage=100'
```

To exercise discard mode, remove the `-upstream` argument from the `regulator`
service and recreate it. The regulator will still count and expose incoming
samples, but it will not connect to Prometheus.

The Alloy image uses `latest` to keep this development example easy to run;
pin it to an organization-approved version for repeatable deployments.

For a three-replica Kubernetes deployment with gossip, see
[`k8s/`](k8s/).
