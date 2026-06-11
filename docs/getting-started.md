# Getting Started

## Prerequisites

- Kubernetes 1.26+
- Helm 3.x
- `kubectl` configured

## Install

```bash
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system --create-namespace \
  --set image.tag=0.5.1
```

Verify the operator is running:

```bash
kubectl -n kmorph-system get pods
kubectl get crd | grep kmorph
```

## Your first profile

### 1. Define a ClusterProfile

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
        tier: backend
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

### 2. Activate it

```yaml
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: fallback
spec:
  profileRef: minimal
  priority: 0
```

```bash
kubectl apply -f profile.yaml
kubectl apply -f activation.yaml
```

### 3. Check status

```bash
kubectl get cp    # ClusterProfiles (short name)
kubectl get pa    # ProfileActivations (short name)
```

```
NAME      DRIFT POLICY   ACTIVATIONS      AGE
minimal   strict         ["fallback"]     30s
```

```
NAME       PROFILE   PRIORITY   PHASE    ACTIVE SINCE
fallback   minimal   0          Active   10s
```

## Next steps

- [Profiles](profiles.md) — patch types, multiple namespaces, labelSelector
- [Activations](activations.md) — priority, schedules, duration, suspend, dry-run mode
- [Argo Rollouts](rollouts.md) — patching Rollout resources
- [Webhooks](webhooks.md) — enable validating webhooks with cert-manager
- [Architecture](architecture.md) — how kmorph works internally
