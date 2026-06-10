# kmorph

**kmorph** is a Kubernetes operator for switching cluster resource profiles between named modes.

It lets you define named resource configurations (profiles) and activate them on demand — instantly or on a schedule. Perfect for keeping staging environments on minimal resources and scaling up to production settings when needed.

## How it works

Two CRDs:

- **`ClusterProfile`** — defines a named set of patches to apply to any Kubernetes resources across multiple namespaces
- **`ProfileActivation`** — activates a profile, optionally with a schedule, duration, and priority

When multiple activations are active simultaneously, the one with the highest **priority** wins. Lower-priority activations stay in `Pending` phase and take over automatically when the winner expires or is deleted.

```
ClusterProfile(sleep)   ←── ProfileActivation(sleep-nights,   priority=20, schedule=18:00-09:00)
ClusterProfile(minimal) ←── ProfileActivation(fallback,       priority=0,  always active)
                         ←── ProfileActivation(business-hours, priority=10, schedule=09:00-18:00)
ClusterProfile(prod)    ←── ProfileActivation(client-demo,    priority=100, duration=4h)
```

## Installation

```bash
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system --create-namespace
```

## Quick start

### 1. Define profiles

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
      labelSelector:
        app: backend
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
      labelSelector:
        app: backend
    patch:
      spec:
        replicas: 1
        template:
          spec:
            containers:
            - name: backend
              resources:
                requests: { cpu: 50m, memory: 64Mi }
```

### 2. Create a fallback activation (always active)

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: fallback
spec:
  profileRef: minimal
  priority: 0
```

### 3. Schedule sleep at night

```yaml
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

### 4. Activate production for a client demo (4 hours)

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: client-demo
spec:
  profileRef: production
  priority: 100
  duration: "4h"
```

Delete to revert immediately:

```bash
kubectl delete profileactivation client-demo
```

## Drift policies

Each `ClusterProfile` has a `driftPolicy`:

| Policy | Behaviour |
|--------|-----------|
| `strict` | Continuously re-applies patches (default: every 30s). Manual changes are reverted. |
| `soft` | Applies once. Detects drift and reports it in `status.driftDetected` but does not revert. |
| `audit` | Never applies. Only reports what would change. |

## Activation phases

| Phase | Meaning |
|-------|---------|
| `Active` | This activation is the current winner and its profile is being applied. |
| `Pending` | In-window but preempted by a higher-priority activation. |
| `Expired` | Duration elapsed. |
| `Suspended` | Paused via `spec.suspended: true`. |

## kubectl reference

```bash
# list profiles and activations
kubectl get cp
kubectl get pa

# activate a profile for 2 hours
kubectl apply -f - <<EOF
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: my-activation
spec:
  profileRef: production
  priority: 50
  duration: "2h"
EOF

# suspend an activation without deleting it
kubectl patch pa my-activation --type=merge -p '{"spec":{"suspended":true}}'

# delete to trigger immediate failover to next priority
kubectl delete pa my-activation
```

## Building

```bash
# build and push image (requires podman)
make image VERSION=0.1.0

# install via helm
make helm-install VERSION=0.1.0
```

## Webhooks (optional)

Validating webhooks block invalid resources at admission time:
- `ProfileActivation` referencing a non-existent `ClusterProfile`
- Invalid cron expressions or timezone
- Invalid duration format
- Deleting a `ClusterProfile` while an activation is `Active`

Webhooks are disabled by default (require TLS). Enable with `--enable-webhooks` flag.

## License

Apache 2.0
