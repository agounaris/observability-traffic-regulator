# Kubernetes example

This example runs three regulator replicas with fixed-peer policy gossip:

```text
Alloy -> regulator Service -> any regulator replica -> Mimir/Thanos
                         \-> headless Service -> regulator-0/1/2 gossip
```

The StatefulSet gives each replica a stable identity and DNS name. The normal
`regulator` Service is for Alloy traffic; `regulator-headless` is only for
replica-to-replica gossip.

## Deploy

Edit `regulator.yaml` first:

1. Replace the container image.
2. Replace the `-upstream` URL.
3. Replace the Secret value.

Then apply it:

```sh
kubectl apply -f regulator.yaml
kubectl get pods -l app.kubernetes.io/name=traffic-regulator -w
```

All replicas use the same fixed peer list. The policy can be changed through
any replica because gossip converges the versioned policy:

```sh
kubectl port-forward service/regulator 8080:8080
curl -XPOST 'http://localhost:8080/api/set?allowed_percentage=25'
curl http://localhost:8080/api/status
```

Configure Alloy to write to the normal Service:

```hcl
prometheus.remote_write "regulator" {
  endpoint {
    url = "http://regulator:8080/api/v1/write"
  }
}
```

For OTLP/HTTP metrics, point an OTLP exporter at the same Service:

```text
http://regulator:8080/v1/metrics
```

Set `-otlp-upstream` on the regulator to the backend OTLP/HTTP metrics endpoint.
If it is omitted, it defaults to `-upstream`; if both are omitted, OTLP data is
accepted, counted, and discarded.

The current gossip state is in memory. For protection against all replicas
restarting simultaneously, persist the policy or move the control state to a
durable external store before relying on this in production.
