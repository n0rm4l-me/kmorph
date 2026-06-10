# kmorph

**kmorph** is a Kubernetes operator for switching cluster resource profiles between named modes.

Define named resource configurations (profiles) and activate them on demand — instantly, on a schedule, or for a fixed duration. Designed for keeping staging environments on minimal resources and scaling to production settings when needed.

Full support for **Argo Rollouts** with canary skip, abort-on-failure, and promote lifecycle management.

## How it works

Two CRDs:

- **`ClusterProfile`** — defines a named set of patches to apply to any Kubernetes resources across multiple namespaces
- **`ProfileActivation`** — activates a profile with priority, optional schedule, and duration

When multiple activations are simultaneously active, the one with the highest **priority** wins. Lower-priority activations stay in `Pending` and take over automatically when the winner expires or is deleted.

```
ClusterProfile(sleep)      ←── ProfileActivation(sleep-nights,   priority=20, schedule=18:00-09:00)
ClusterProfile(minimal)    ←── ProfileActivation(fallback,       priority=0,  always active)
                            ←── ProfileActivation(business-hours, priority=10, schedule=09:00-18:00)
ClusterProfile(production) ←── ProfileActivation(client-demo,    priority=100, duration=4h)
```

## Installation

```bash
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system --create-namespace \
  --set image.repository=<your-registry>/kmorph \
  --set image.tag=0.4.5
```

## Quick start

### 1. Define profiles

```yaml
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
                limits:   { cpu: 200m, memory: 128Mi }
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

### 4. Activate production for a fixed duration

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

## Patch types

Each patch entry supports three `patchType` values:

| patchType | RFC | Best for |
|-----------|-----|----------|
| `strategic` (default) | — | Native k8s resources: Deployment, StatefulSet, DaemonSet |
| `merge` | RFC 7396 | Custom CRDs where strategic merge is not supported (e.g. Argo Rollout `spec.replicas`, `nodeSelector`) |
| `json` | RFC 6902 | Surgical operations on specific paths — ideal for patching `containers[0].resources` without touching `image` |

```yaml
patches:
# merge patch — replicas and nodeSelector on a Rollout
- target:
    namespace: my-app
    kind: Rollout
    group: argoproj.io
    name: my-service
  patchType: merge
  patch:
    spec:
      replicas: 2

# json patch — container resources only, image untouched
- target:
    namespace: my-app
    kind: Rollout
    group: argoproj.io
    name: my-service
  patchType: json
  patch:
  - op: replace
    path: /spec/template/spec/containers/0/resources/requests/cpu
    value: "500m"
  - op: replace
    path: /spec/template/spec/containers/0/resources/requests/memory
    value: "768Mi"
```

## Argo Rollout support

kmorph has full first-class support for Argo Rollouts via `rolloutPolicy`.

### How it works

When `rolloutPolicy` is set on a patch targeting a Rollout:

1. **PREPARE** — sets `argoproj.io/skip-steps: "true"` annotation on the Rollout object
2. **PATCH** — applies all patches (replicas, resources, nodeSelector, etc.)
3. **PROMOTE** — calls `promoteFull` immediately after all patches, skipping canary analysis and pauses
4. **WATCH** — monitors promote phase; on `Degraded` triggers abort and optional revert
5. **CLEANUP** — removes `skip-steps` annotation after successful promote

### Example

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: production
spec:
  driftPolicy: strict
  patches:
  # patch 1: replicas via merge (rolloutPolicy triggers promoteFull after all patches)
  - target:
      namespace: my-app
      kind: Rollout
      group: argoproj.io
      name: my-service
    patchType: merge
    rolloutPolicy:
      skipSteps: true            # skip canary pipeline via promoteFull
      abortOnFailure: true       # abort and revert if Rollout goes Degraded
      progressDeadlineSeconds: 300
    patch:
      spec:
        replicas: 3

  # patch 2: container resources via json patch (surgical, doesn't touch image)
  - target:
      namespace: my-app
      kind: Rollout
      group: argoproj.io
      name: my-service
    patchType: json
    patch:
    - op: replace
      path: /spec/template/spec/containers/0/resources/requests/cpu
      value: "500m"
    - op: replace
      path: /spec/template/spec/containers/0/resources/requests/memory
      value: "768Mi"
    - op: replace
      path: /spec/template/spec/containers/0/resources/limits/cpu
      value: "500m"
    - op: replace
      path: /spec/template/spec/containers/0/resources/limits/memory
      value: "768Mi"
```

### rolloutPolicy fields

| Field | Default | Description |
|-------|---------|-------------|
| `skipSteps` | `false` | Skip all canary analysis and pauses via `promoteFull` |
| `abortOnFailure` | `true` | Abort rollout if it transitions to Degraded during promote |
| `progressDeadlineSeconds` | `600` | Timeout before treating promote as failed |

### Known GVKs

kmorph resolves versions for common CRDs automatically:

| Group | Kind | Version |
|-------|------|---------|
| `argoproj.io` | `Rollout` | `v1alpha1` |
| `argoproj.io` | `AnalysisRun` | `v1alpha1` |
| `keda.sh` | `ScaledObject` | `v1alpha1` |
| `keda.sh` | `ScaledJob` | `v1alpha1` |

For other CRDs, specify `group` and kmorph falls back to `v1alpha1`. For core group resources omit `group`.

## Drift policies

| Policy | Behaviour |
|--------|-----------|
| `strict` | Continuously re-applies patches (default: every 30s). Manual changes are reverted. For Rollouts: only `spec.replicas` is checked to avoid triggering spurious revisions. |
| `soft` | Applies once. Detects drift and reports it in `status.driftDetected` but does not revert. |
| `audit` | Never applies. Only reports what would change. |

## Activation phases

| Phase | Meaning |
|-------|---------|
| `Active` | This activation is the current winner and its profile is being applied. |
| `Pending` | In-window but preempted by a higher-priority activation. Will take over when winner expires/is deleted. |
| `Expired` | Duration elapsed. |
| `Suspended` | Paused via `spec.suspended: true`. |

## ProfileActivation status

```yaml
status:
  phase: Active
  activeProfile: production
  activeSince: "2026-06-10T09:00:00Z"
  expiresAt: "2026-06-10T13:00:00Z"      # set when duration is specified
  nextTransition: "2026-06-10T18:00:00Z" # next schedule event
  lastAppliedTime: "2026-06-10T09:00:05Z"
  driftDetected: []                        # filled in soft/audit mode
  rolloutProgress:                         # Rollout promote tracking
  - namespace: my-app
    name: my-service
    phase: Healthy
    previousStableRS: "abc123"
    patchedAt: "2026-06-10T09:00:05Z"
    completedAt: "2026-06-10T09:01:30Z"
    message: "promote completed successfully"
```

## kubectl reference

```bash
# list profiles and activations (short names)
kubectl get cp
kubectl get pa

# activate a profile for 2 hours with high priority
kubectl apply -f - <<EOF
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: my-activation
spec:
  profileRef: production
  priority: 100
  duration: "2h"
EOF

# suspend an activation without deleting it
kubectl patch pa my-activation --type=merge -p '{"spec":{"suspended":true}}'

# resume
kubectl patch pa my-activation --type=merge -p '{"spec":{"suspended":false}}'

# delete to trigger immediate failover to next priority
kubectl delete pa my-activation

# check rollout promote status
kubectl get pa my-activation -o jsonpath='{.status.rolloutProgress}' | jq .
```

## Building

```bash
# build and push image (requires podman)
make image VERSION=0.4.5

# install via helm
make helm-install VERSION=0.4.5
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
