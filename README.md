# kmorph

<p align="center">
  <img src="https://img.shields.io/badge/kubernetes-operator-326CE5?logo=kubernetes&logoColor=white" alt="Kubernetes Operator"/>
  <img src="https://img.shields.io/github/v/release/n0rm4l-me/kmorph?sort=semver&logo=github" alt="Release"/>
  <img src="https://img.shields.io/github/actions/workflow/status/n0rm4l-me/kmorph/ci.yaml?branch=main&label=CI&logo=github-actions" alt="CI"/>
  <img src="https://img.shields.io/github/actions/workflow/status/n0rm4l-me/kmorph/e2e.yaml?branch=main&label=E2E&logo=github-actions" alt="E2E"/>
  <img src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go" alt="Go Version"/>
  <img src="https://img.shields.io/badge/license-Apache%202.0-blue" alt="License"/>
  <img src="https://img.shields.io/badge/helm-3-0F1689?logo=helm" alt="Helm"/>
</p>

<p align="center">
  <b>A Kubernetes operator for switching cluster resource profiles — on demand, on a schedule, or for a fixed duration.</b>
</p>

---

## Why kmorph?

Running Kubernetes environments at full production capacity 24/7 is expensive. Most staging and development environments sit idle overnight, on weekends, and between test cycles — yet they keep consuming CPU, memory, and cloud budget.

kmorph solves this by letting you define **named resource profiles** and switch between them instantly. One command puts the entire environment to sleep. Another wakes it to production specs for a client demo. A cron schedule does it automatically every night.

```
                     priority wins
┌─────────────────────────────────────────────────┐
│  ProfileActivation: sleep-nights   priority=20  │  ← Active 18:00-09:00
│  ProfileActivation: business-hours priority=10  │  ← Active 09:00-18:00
│  ProfileActivation: client-demo    priority=100 │  ← Temporary override, expires in 4h
│  ProfileActivation: fallback       priority=0   │  ← Always-on safety net
└─────────────────────────────────────────────────┘
         ↓ highest priority wins
  ClusterProfile applied to cluster resources
```

---

## Features

### Core
- **Priority-based activation** — multiple activations coexist; the highest priority wins automatically
- **Cron schedules** with timezone support — `start`/`end` expressions, any IANA timezone
- **Time-limited activations** — `duration: "4h"` auto-expires and fails over to next priority
- **Suspend/resume** — pause an activation without deleting it
- **Drift enforcement** — `strict` mode continuously re-applies patches; manual changes are reverted within 30s
- **Audit and soft modes** — preview what would change or detect drift without enforcing

### Patching
- **Any Kubernetes resource** — Deployment, StatefulSet, DaemonSet, CronJob, ConfigMap, and more
- **Custom CRDs** — Argo Rollouts, KEDA ScaledObject, or any CRD (auto-resolves known versions)
- **Three patch types** — `strategic` (native k8s), `merge` (RFC 7396), `json` (RFC 6902 — surgical, preserves `image`)
- **Multi-namespace profiles** — one profile controls resources across multiple namespaces

### Argo Rollouts
- **Full canary lifecycle management** — pause before patch → apply → `promoteFull` → watch → cleanup
- **Race condition prevention** — Rollout is paused before template changes so Analysis Runs never start
- **Abort on failure** — reverts to `previousStableRS` if Rollout goes Degraded
- **JSON patch for resources** — patches `containers[0].resources` without touching `image`

### Operations
- **Dry-run mode** — `spec.dryRun: true` previews changes server-side without applying anything
- **Kubernetes Events** — Activated, Preempted, Expired, DriftDetected, RolloutPromoted, RolloutFailed
- **Prometheus metrics** — active/pending activations, patch rates, rollout durations, drift events
- **Google Managed Prometheus** — optional `PodMonitoring` CRD for GKE clusters
- **Validating webhooks** — reject invalid resources at admission time (cert-manager integration)
- **Finalizers** — safe cleanup of Rollout annotations on activation deletion
- **Immediate reconcile** — ClusterProfile changes trigger instant reconciliation, no 30s wait

---

## Use Cases

### 💤 Overnight sleep schedule

Put your entire staging environment to sleep at 18:00 and wake it at 09:00 on weekdays. Saves ~65% on cloud costs.

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: sleep
spec:
  driftPolicy: strict
  patches:
  - target:
      namespace: staging
      kind: Deployment
      labelSelector: { tier: app }
    patch:
      spec:
        replicas: 0
  - target:
      namespace: staging
      kind: CronJob
      labelSelector: { tier: batch }
    patchType: merge
    patch:
      spec:
        suspend: true
---
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
```

### 🚀 Client demo in production mode

Scale up to production replica counts and resources for a time-limited window. Automatically reverts when the timer expires.

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: client-demo
spec:
  profileRef: production      # full replica counts, bigger instances
  priority: 100               # overrides sleep schedule
  duration: "4h"              # auto-expires, fallback takes over
```

```bash
# When demo is over — delete to revert immediately
kubectl delete pa client-demo
```

### 🔄 Argo Rollouts — switch resources without triggering canary

Change replicas and CPU/memory on a canary-enabled Rollout without running smoke tests or analysis. kmorph pauses the Rollout, patches it, then calls `promoteFull`.

```yaml
patches:
# Merge patch: replicas + node class
- target:
    namespace: prod
    kind: Rollout
    group: argoproj.io
    name: api-service
  patchType: merge
  rolloutPolicy:
    skipSteps: true
    abortOnFailure: true
  patch:
    spec:
      replicas: 3
      template:
        spec:
          nodeSelector:
            cloud.google.com/compute-class: Scale-Out

# JSON patch: resources only (image untouched)
- target:
    namespace: prod
    kind: Rollout
    group: argoproj.io
    name: api-service
  patchType: json
  patch:
  - { op: replace, path: /spec/template/spec/containers/0/resources/requests/cpu,    value: "2" }
  - { op: replace, path: /spec/template/spec/containers/0/resources/requests/memory, value: "8Gi" }
```

### 🔍 Preview before applying

Validate what a production profile would change in your staging cluster before activating it. No real changes made.

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: preview-production
spec:
  profileRef: production
  dryRun: true              # server-side dry-run, never applies
```

```bash
kubectl get pa preview-production -o jsonpath='{.status.dryRunResult}' | jq .
# {
#   "summary": "5 of 12 resource(s) would change",
#   "changes": [
#     { "name": "api", "changed": true,
#       "diff": "~ spec.replicas: 1 → 3\n~ containers[0].resources.requests.cpu: 500m → 2" }
#   ]
# }
```

### 🛡️ Emergency shutdown

Override everything with highest priority — bypasses all schedules, demos, and fallbacks.

```bash
kubectl apply -f - <<EOF
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: emergency-shutdown
spec:
  profileRef: sleep
  priority: 999
EOF

# Undo: delete the activation
kubectl delete pa emergency-shutdown
```

---

## Install

```bash
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system \
  --create-namespace \
  --set image.tag=0.5.6
```

## Quick Start

```yaml
# 1. Define profiles
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: minimal
spec:
  driftPolicy: strict
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
            - name: app
              resources:
                requests: { cpu: 50m, memory: 64Mi }
---
# 2. Activate it
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: fallback
spec:
  profileRef: minimal
  priority: 0
```

```bash
kubectl get cp    # ClusterProfiles
kubectl get pa    # ProfileActivations
```

---

## Documentation

| Guide | Description |
|-------|-------------|
| [Getting Started](docs/getting-started.md) | Installation, first profile, basic usage |
| [Profiles](docs/profiles.md) | ClusterProfile spec, patch types, examples |
| [Activations](docs/activations.md) | Priority, schedules, duration, suspend, dry-run |
| [Argo Rollouts](docs/rollouts.md) | Full Rollout support with canary skip |
| [Architecture](docs/architecture.md) | How kmorph works internally |
| [Webhooks](docs/webhooks.md) | Validating webhooks with cert-manager |
| [Observability](docs/observability.md) | Metrics, GMP, k9s integration |
| [Helm Values](docs/helm-values.md) | Full values reference |

---

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) · [Security Policy](SECURITY.md) · [Code of Conduct](CODE_OF_CONDUCT.md)

## License

Apache 2.0
