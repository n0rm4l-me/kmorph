# Observability

## Prometheus Metrics

Metrics are exposed on `:8080/metrics` by default.

### kmorph-specific metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `kmorph_active_activations_total` | Gauge | — | ProfileActivations currently Active |
| `kmorph_pending_activations_total` | Gauge | — | ProfileActivations currently Pending |
| `kmorph_drift_events_total` | Counter | `profile`, `drift_policy` | Drift detection events |
| `kmorph_patch_applied_total` | Counter | `profile`, `namespace`, `kind`, `patch_type` | Successful patches |
| `kmorph_patch_failed_total` | Counter | `profile`, `namespace`, `kind` | Failed patches |
| `kmorph_rollout_promote_duration_seconds` | Histogram | `namespace`, `rollout`, `result` | Rollout promote duration |
| `kmorph_activation_switch_total` | Counter | `profile`, `reason` | Activation winner changes |

### controller-runtime standard metrics

| Metric | Description |
|--------|-------------|
| `controller_runtime_reconcile_total` | Total reconciliations by result (success/error/requeue) |
| `controller_runtime_reconcile_errors_total` | Reconciliation errors |
| `controller_runtime_reconcile_time_seconds` | Reconciliation duration histogram |
| `workqueue_depth` | Items waiting in reconcile queue |

### Access metrics locally

```bash
kubectl -n kmorph-system port-forward svc/kmorph-metrics 8080:8080
curl localhost:8080/metrics | grep kmorph_
```

## Google Managed Prometheus (GKE)

Enable PodMonitoring to automatically scrape metrics into Google Cloud Monitoring:

```bash
helm upgrade --install kmorph charts/kmorph \
  --set podMonitoring.enabled=true \
  --set podMonitoring.interval=30s
```

This creates a `PodMonitoring` resource (`monitoring.googleapis.com/v1`) that GMP picks up automatically. Metrics appear in Cloud Monitoring under the `prometheus.googleapis.com` prefix.

## Prometheus Operator (non-GKE)

For clusters with the Prometheus Operator, create a `ServiceMonitor` manually:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kmorph
  namespace: kmorph-system
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: kmorph
  endpoints:
  - port: metrics
    interval: 30s
    path: /metrics
```

## Health endpoints

| Endpoint | Port | Description |
|----------|------|-------------|
| `/healthz` | 8081 | Liveness — process is alive |
| `/readyz` | 8081 | Readiness — informer caches synced |

```bash
kubectl -n kmorph-system port-forward pod/<pod> 8081:8081
curl localhost:8081/healthz
curl localhost:8081/readyz
```

## k9s Integration

See [docs/k9s-plugins.yaml](k9s-plugins.yaml) for plugin definitions.

Navigate in k9s:
- `:cp` — ClusterProfiles
- `:pa` — ProfileActivations

## Kubernetes Events

kmorph emits standard Kubernetes Events on all state transitions:

| Reason | Type | Trigger |
|--------|------|---------|
| `Activated` | Normal | Activation becomes Active |
| `Preempted` | Normal | Activation displaced by higher priority |
| `Suspended` | Normal | `spec.suspended=true` |
| `Expired` | Normal | Duration elapsed |
| `DriftDetected` | Warning | Resources drifted in soft/audit mode |
| `RolloutPatched` | Normal | Rollout patch applied, promote started |
| `RolloutPromoted` | Normal | Rollout promote completed |
| `RolloutFailed` | Warning | Rollout went Degraded or timed out |
| `RolloutReverted` | Warning | Reverted to previousStableRS |
| `Deleted` | Normal | Activation deleted, finalizer cleanup done |

```bash
kubectl describe pa my-activation
kubectl get events --field-selector involvedObject.name=my-activation
```
