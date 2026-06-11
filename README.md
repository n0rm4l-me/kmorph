# kmorph

<p align="center">
  <img src="https://img.shields.io/badge/kubernetes-operator-326CE5?logo=kubernetes&logoColor=white" alt="Kubernetes Operator"/>
  <img src="https://img.shields.io/github/v/release/n0rm4l-me/kmorph?sort=semver&logo=github" alt="Release"/>
  <img src="https://img.shields.io/github/actions/workflow/status/n0rm4l-me/kmorph/ci.yaml?branch=main&label=CI&logo=github-actions" alt="CI"/>
  <img src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go" alt="Go Version"/>
  <img src="https://img.shields.io/badge/license-Apache%202.0-blue" alt="License"/>
  <img src="https://img.shields.io/badge/CRD-v1alpha1-orange" alt="API Version"/>
  <img src="https://img.shields.io/badge/helm-3-0F1689?logo=helm" alt="Helm"/>
</p>

<p align="center">
  <b>Switch your Kubernetes cluster between resource profiles on demand, on a schedule, or for a fixed duration.</b>
</p>

---

## Overview

kmorph is a Kubernetes operator that lets you define named resource configurations (**ClusterProfiles**) and activate them through **ProfileActivations** — with priority-based conflict resolution, cron schedules, time-limited activations, and full Argo Rollouts support.

**Perfect for:**
- Keeping staging environments on minimal resources and scaling to production on demand
- Scheduling sleep/wake cycles to reduce cloud costs overnight
- Temporarily activating production-level resources for client demos or load tests
- Managing multiple environments with a single declarative configuration

```
ClusterProfile(sleep)      ←── ProfileActivation(sleep-nights,   priority=20, schedule=18:00-09:00 JST)
ClusterProfile(minimal)    ←── ProfileActivation(fallback,       priority=0,  always active)
                            ←── ProfileActivation(business-hours, priority=10, schedule=09:00-18:00 JST)
ClusterProfile(production) ←── ProfileActivation(client-demo,    priority=100, duration=4h)
```

When multiple activations are simultaneously eligible, the one with the **highest priority wins**. Lower-priority activations automatically take over when the winner expires or is deleted.

---

## Features

- **Priority-based activation** — multiple activations can coexist; highest priority wins
- **Cron schedules with timezone support** — `schedule.start`, `schedule.end`, `schedule.timezone`
- **Time-limited activations** — `duration: "4h"` auto-expires and fails over
- **Suspend/resume** — pause without deleting
- **Three drift policies** — `strict` (enforce), `soft` (report), `audit` (observe)
- **Three patch types** — `strategic`, `merge` (RFC 7396), `json` (RFC 6902)
- **Full Argo Rollout support** — `rolloutPolicy` with `skipSteps`, `abortOnFailure`, `progressDeadlineSeconds`
- **Kubernetes Events** — on Activated, Preempted, Expired, DriftDetected, RolloutPromoted
- **Finalizers** — safe cleanup of Rollout annotations on deletion
- **Prometheus metrics** on `:8080` (controller-runtime standard + custom)
- **Google Managed Prometheus** — optional `PodMonitoring` CRD in Helm chart

---

## Quick Start

### Install

```bash
helm upgrade --install kmorph oci://ghcr.io/n0rm4l-me/charts/kmorph \
  --namespace kmorph-system --create-namespace \
  --version 0.5.0
```

Or from source:

```bash
git clone https://github.com/n0rm4l-me/kmorph
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system --create-namespace \
  --set image.tag=0.5.0
```

### Define profiles

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: sleep
spec:
  driftPolicy: strict
  patches:
  - target:
      namespace: my-app
      kind: Deployment
      labelSelector: { tier: backend }
    patch:
      spec:
        replicas: 0
---
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: minimal
spec:
  patches:
  - target:
      namespace: my-app
      kind: Deployment
      labelSelector: { tier: backend }
    patch:
      spec:
        replicas: 1
        template:
          spec:
            containers:
            - name: backend
              resources:
                requests: { cpu: 50m, memory: 64Mi }
---
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: production
spec:
  patches:
  - target:
      namespace: my-app
      kind: Deployment
      labelSelector: { tier: backend }
    patch:
      spec:
        replicas: 5
        template:
          spec:
            containers:
            - name: backend
              resources:
                requests: { cpu: 500m, memory: 512Mi }
```

### Create activations

```yaml
# Always-on fallback
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: fallback
spec:
  profileRef: minimal
  priority: 0
---
# Sleep overnight (JST)
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: sleep-nights
spec:
  profileRef: sleep
  priority: 20
  schedule:
    start: "0 18 * * 1-5"
    end:   "0 9  * * 1-5"
    timezone: "Asia/Tokyo"
---
# Temporary production boost (auto-expires)
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: demo
spec:
  profileRef: production
  priority: 100
  duration: "4h"
```

```bash
# Check status
kubectl get cp    # ClusterProfiles
kubectl get pa    # ProfileActivations

# Revert by deleting — next priority takes over automatically
kubectl delete pa demo
```

---

## Argo Rollouts Support

kmorph has first-class support for Argo Rollouts via `rolloutPolicy`:

```yaml
patches:
# Merge patch: replicas + promote lifecycle
- target:
    namespace: my-app
    kind: Rollout
    group: argoproj.io
    name: my-service
  patchType: merge
  rolloutPolicy:
    skipSteps: true             # full promote, skip canary analysis
    abortOnFailure: true        # revert to previousStableRS on Degraded
    progressDeadlineSeconds: 300
  patch:
    spec:
      replicas: 3

# JSON patch: surgical resource update (image untouched)
- target:
    namespace: my-app
    kind: Rollout
    group: argoproj.io
    name: my-service
  patchType: json
  patch:
  - { op: replace, path: /spec/template/spec/containers/0/resources/requests/cpu, value: "500m" }
  - { op: replace, path: /spec/template/spec/containers/0/resources/requests/memory, value: "768Mi" }
```

**Lifecycle:** PREPARE (set skip-steps) → PATCH → PROMOTE (promoteFull) → WATCH → CLEANUP (remove skip-steps)

---

## Patch Types

| `patchType` | RFC | Best for |
|-------------|-----|----------|
| `strategic` (default) | — | Native k8s: Deployment, StatefulSet, DaemonSet |
| `merge` | RFC 7396 | Custom CRDs: Rollout, ScaledObject |
| `json` | RFC 6902 | Surgical path operations — patch `containers[0].resources` without touching `image` |

---

## Drift Policies

| Policy | Behaviour |
|--------|-----------|
| `strict` | Re-applies patches every 30s. Manual changes are reverted. |
| `soft` | Applies once. Reports drift in `status.driftDetected`. |
| `audit` | Never applies. Only reports what would change. |

---

## Activation Phases

| Phase | Meaning |
|-------|---------|
| `Active` | Winner — profile is being applied. |
| `Pending` | In-window but preempted by higher priority. Takes over when winner expires/is deleted. |
| `Expired` | Duration elapsed. |
| `Suspended` | Paused via `spec.suspended: true`. |

---

## ProfileActivation Status

```yaml
status:
  phase: Active
  activeProfile: production
  activeSince: "2026-06-10T09:00:00Z"
  expiresAt: "2026-06-10T13:00:00Z"
  nextTransition: "2026-06-10T18:00:00Z"
  lastAppliedTime: "2026-06-10T09:00:05Z"
  driftDetected: []
  rolloutProgress:
  - namespace: my-app
    name: my-service
    phase: Healthy
    previousStableRS: abc123
    patchedAt: "2026-06-10T09:00:05Z"
    completedAt: "2026-06-10T09:01:30Z"
    message: "promote completed successfully"
```

---

## Helm Values

| Value | Default | Description |
|-------|---------|-------------|
| `image.repository` | `ghcr.io/n0rm4l-me/kmorph` | Image repository |
| `image.tag` | `0.5.0` | Image tag |
| `replicaCount` | `1` | Number of replicas |
| `leaderElection.enabled` | `true` | Enable leader election (required for >1 replica) |
| `resources.requests.cpu` | `50m` | CPU request |
| `resources.requests.memory` | `64Mi` | Memory request |
| `resources.limits.cpu` | `200m` | CPU limit |
| `resources.limits.memory` | `128Mi` | Memory limit |
| `podMonitoring.enabled` | `false` | Create GMP `PodMonitoring` resource |
| `podMonitoring.interval` | `30s` | Scrape interval |

---

## Observability

### Prometheus Metrics

Metrics are exposed on `:8080/metrics` by default. Standard controller-runtime metrics are included:

```
# Reconciliation duration
controller_runtime_reconcile_time_seconds_bucket{controller="profileactivation", ...}

# Error rate
controller_runtime_reconcile_errors_total{controller="profileactivation"}

# Queue depth
workqueue_depth{name="profileactivation"}
```

### Google Managed Prometheus (GKE)

```bash
helm upgrade --install kmorph charts/kmorph \
  --set podMonitoring.enabled=true
```

### k9s Integration

See [docs/k9s-plugins.yaml](docs/k9s-plugins.yaml) for k9s plugin integration.

Navigate to `:cp` (ClusterProfiles) or `:pa` (ProfileActivations) in k9s.

---

## Known GVKs

kmorph resolves versions for common CRDs automatically:

| Group | Kind | Version |
|-------|------|---------|
| `apps` | `Deployment`, `StatefulSet`, `DaemonSet` | `v1` |
| `argoproj.io` | `Rollout`, `AnalysisRun` | `v1alpha1` |
| `keda.sh` | `ScaledObject`, `ScaledJob` | `v1alpha1` |

Unknown CRDs fall back to `v1alpha1`. For core resources omit `group`.

---

## Development

```bash
# Prerequisites: Go 1.25+, kubebuilder 4.x, podman

# Generate CRDs and deepcopy
make generate manifests

# Run tests
make test

# Build and push image
make image VERSION=0.5.0

# Install via Helm
make helm-install VERSION=0.5.0
```

### Webhooks (optional)

Validating webhooks require TLS certificates. Enable with:

```bash
helm upgrade --install kmorph charts/kmorph \
  --set webhooks.enabled=true
```

Or run with `--enable-webhooks` flag (requires cert-manager or manual TLS setup).

---

## Roadmap

- [ ] `v1beta1` API with conversion webhooks
- [ ] Webhooks enabled by default (with cert-manager integration)
- [ ] Dry-run mode — preview patches without applying
- [ ] Multi-tenancy — namespace-scoped profiles
- [ ] Audit logging
- [ ] SBOM + signed releases (cosign)

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) · [Code of Conduct](CODE_OF_CONDUCT.md) · [Security Policy](SECURITY.md)

---

## License

Apache 2.0 — see [LICENSE](LICENSE)
