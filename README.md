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
  <b>Switch your Kubernetes cluster between resource profiles — on demand, on a schedule, or for a fixed duration.</b>
</p>

---

## What is kmorph?

kmorph is a Kubernetes operator that lets you define named resource configurations (**ClusterProfiles**) and activate them through **ProfileActivations**. Multiple activations are resolved by priority — the highest priority wins, and lower-priority activations automatically take over when the winner expires or is deleted.

**Built for:** staging cost reduction, client demos, scheduled sleep/wake cycles, and production scaling on demand.

```
ClusterProfile(sleep)      ←── ProfileActivation(sleep-nights,   priority=20, schedule=18:00-09:00)
ClusterProfile(minimal)    ←── ProfileActivation(fallback,       priority=0,  always active)
ClusterProfile(production) ←── ProfileActivation(client-demo,    priority=100, duration=4h)
```

## Install

```bash
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system --create-namespace \
  --set image.tag=0.5.1
```

## Quick example

```yaml
# Define a profile
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
---
# Activate it
apiVersion: config.kmorph.io/v1alpha1
kind: ProfileActivation
metadata:
  name: fallback
spec:
  profileRef: minimal
  priority: 0
```

```bash
kubectl get cp   # ClusterProfiles
kubectl get pa   # ProfileActivations
```

## Documentation

| | |
|---|---|
| [Getting Started](docs/getting-started.md) | Installation, first profile, basic usage |
| [Profiles](docs/profiles.md) | ClusterProfile spec, patch types, examples |
| [Activations](docs/activations.md) | Priority, schedules, duration, suspend |
| [Argo Rollouts](docs/rollouts.md) | Full Rollout support with canary skip |
| [Architecture](docs/architecture.md) | How kmorph works internally |
| [Webhooks](docs/webhooks.md) | Validating webhooks with cert-manager |
| [Observability](docs/observability.md) | Metrics, GMP, k9s integration |
| [Helm Values](docs/helm-values.md) | Full values reference |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) · [Security Policy](SECURITY.md) · [Code of Conduct](CODE_OF_CONDUCT.md)

## License

Apache 2.0
