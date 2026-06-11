# Helm Values Reference

## Install

```bash
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system --create-namespace \
  --set image.tag=0.5.1
```

## All values

```yaml
# Number of operator replicas.
# Use 2+ with leaderElection.enabled=true for HA.
replicaCount: 1

image:
  repository: ghcr.io/n0rm4l-me/kmorph
  tag: "0.5.1"
  pullPolicy: IfNotPresent

imagePullSecrets: []

serviceAccount:
  create: true
  name: ""
  annotations: {}   # useful for Workload Identity on GKE

resources:
  requests:
    cpu: 50m
    memory: 64Mi
  limits:
    cpu: 200m
    memory: 128Mi

nodeSelector: {}
tolerations: []
affinity: {}

# Leader election — required when replicaCount > 1.
leaderElection:
  enabled: true

# PodMonitoring for Google Managed Prometheus.
# Requires monitoring.googleapis.com/v1 CRD (GKE with GMP enabled).
podMonitoring:
  enabled: false
  interval: 30s
```

## Common overrides

### Private registry with Workload Identity

```bash
helm upgrade --install kmorph charts/kmorph \
  --set image.repository=asia-docker.pkg.dev/my-project/my-repo/kmorph \
  --set image.tag=0.5.1 \
  --set serviceAccount.annotations."iam\.gke\.io/gcp-service-account"=kmorph@my-project.iam.gserviceaccount.com
```

### High availability (2 replicas)

```bash
helm upgrade --install kmorph charts/kmorph \
  --set replicaCount=2 \
  --set leaderElection.enabled=true
```

### Enable GMP metrics scraping

```bash
helm upgrade --install kmorph charts/kmorph \
  --set podMonitoring.enabled=true \
  --set podMonitoring.interval=15s
```

### Restrict to specific nodes

```bash
helm upgrade --install kmorph charts/kmorph \
  --set nodeSelector."kubernetes\.io/arch"=amd64 \
  --set tolerations[0].key=dedicated \
  --set tolerations[0].value=operators \
  --set tolerations[0].effect=NoSchedule
```

## Operator flags

Additional flags can be passed to the manager binary via Helm. Edit the Deployment directly or override `args` in values:

| Flag | Default | Description |
|------|---------|-------------|
| `--leader-elect` | auto from `leaderElection.enabled` | Enable leader election |
| `--metrics-bind-address` | `:8080` | Metrics endpoint address |
| `--health-probe-bind-address` | `:8081` | Health/readiness endpoint |
| `--enable-webhooks` | `false` | Enable validating webhooks (requires TLS) |
