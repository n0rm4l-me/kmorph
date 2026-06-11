# ClusterProfile

A `ClusterProfile` defines a named set of patches to apply to Kubernetes resources when activated.

## Spec

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: production
spec:
  driftPolicy: strict      # strict | soft | audit
  patches:
  - target: ...
    patch: ...
    patchType: merge       # strategic | merge | json
    rolloutPolicy: ...     # optional, Argo Rollouts only
```

## Drift policies

| Policy | Behaviour |
|--------|-----------|
| `strict` *(default)* | Re-applies patches every 30s. Manual changes are reverted automatically. |
| `soft` | Applies once on activation. Detects drift and reports it in `status.driftDetected` but never reverts. |
| `audit` | Never applies patches. Only reports what would change in `status.driftDetected`. Use to preview before enabling. |

## Patch types

| `patchType` | RFC | Best for |
|-------------|-----|----------|
| `strategic` *(default)* | — | Native k8s resources: Deployment, StatefulSet, DaemonSet, ConfigMap |
| `merge` | RFC 7396 | Custom CRDs where strategic merge is not supported: Rollout, ScaledObject |
| `json` | RFC 6902 | Surgical operations on specific paths — ideal for patching `containers[0].resources` without touching other fields like `image` |

## Target selection

Targets can be selected by **name** or **labelSelector**:

```yaml
# By name
target:
  namespace: my-app
  kind: Deployment
  name: api-server

# By label selector (all matching resources)
target:
  namespace: my-app
  kind: Deployment
  labelSelector:
    tier: backend
    env: staging
```

For custom CRDs, specify `group`:

```yaml
target:
  namespace: my-app
  kind: Rollout
  group: argoproj.io
  name: api-rollout
```

## Known GVKs

kmorph resolves versions automatically for:

| Group | Kind | Version |
|-------|------|---------|
| `apps` | `Deployment`, `StatefulSet`, `DaemonSet` | `v1` |
| `argoproj.io` | `Rollout`, `AnalysisRun` | `v1alpha1` |
| `keda.sh` | `ScaledObject`, `ScaledJob` | `v1alpha1` |

Unknown CRDs fall back to `v1alpha1`.

## Examples

### Deployment — replicas and resources

```yaml
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
              limits:   { cpu: 200m, memory: 128Mi }
```

### Multiple namespaces in one profile

```yaml
patches:
- target:
    namespace: app-ns
    kind: Deployment
    labelSelector: { app: api }
  patch:
    spec:
      replicas: 0

- target:
    namespace: db-ns
    kind: StatefulSet
    name: postgres
  patch:
    spec:
      replicas: 0

- target:
    namespace: worker-ns
    kind: Deployment
    labelSelector: { app: worker }
  patch:
    spec:
      replicas: 0
```

### JSON patch — surgical resource update

Use `patchType: json` to modify specific paths without replacing the entire field:

```yaml
- target:
    namespace: my-app
    kind: Rollout
    group: argoproj.io
    name: api
  patchType: json
  patch:
  - op: replace
    path: /spec/template/spec/containers/0/resources/requests/cpu
    value: "500m"
  - op: replace
    path: /spec/template/spec/containers/0/resources/requests/memory
    value: "768Mi"
  - op: replace
    path: /spec/template/spec/nodeSelector/cloud.google.com~1gke-spot
    value: "true"
```

> **Note:** Use `~1` to escape `/` in JSON Pointer paths.

### KEDA ScaledObject — pause autoscaling

```yaml
- target:
    namespace: my-app
    kind: ScaledObject
    group: keda.sh
    name: api-scaler
  patchType: merge
  patch:
    metadata:
      annotations:
        autoscaling.keda.sh/paused: "true"
```
