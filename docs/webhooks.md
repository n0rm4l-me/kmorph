# Validating Webhooks

kmorph includes optional validating webhooks that reject invalid resources at admission time — before they reach the reconcile loop.

## What webhooks validate

### ClusterProfile

| Rule | Error |
|------|-------|
| At least one patch required | `spec.patches: Required value` |
| Each patch target needs `namespace` | `spec.patches[0].target.namespace: Required value` |
| Each patch target needs `kind` | `spec.patches[0].target.kind: Required value` |
| Either `name` or `labelSelector` must be set | `spec.patches[0].target: Invalid value` |
| Patch must not be empty | `spec.patches[0].patch: Required value` |
| Cannot delete a ClusterProfile while an activation is Active | `cannot delete ClusterProfile "X": ProfileActivation "Y" is currently active` |

### ProfileActivation

| Rule | Error |
|------|-------|
| `profileRef` must reference an existing ClusterProfile | `spec.profileRef: Invalid value: "X": ClusterProfile "X" not found` |
| `duration` must be a valid Go duration | `spec.duration: Invalid value: "bad": invalid duration` |
| `schedule.start` must be a valid cron expression | `spec.schedule.start: Invalid value: "bad": invalid cron expression` |
| `schedule.end` must be a valid cron expression | `spec.schedule.end: Invalid value` |
| `schedule.timezone` must be a valid IANA timezone | `spec.schedule.timezone: Invalid value: "Mars/Olympus": invalid timezone` |

## Enable with cert-manager (recommended)

cert-manager automatically manages TLS certificates for the webhook server.

### Prerequisites

```bash
# Install cert-manager with correct leader election namespace for GKE Autopilot
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --set crds.enabled=true \
  --set global.leaderElection.namespace=cert-manager
```

> **GKE Autopilot note:** cert-manager must use `leaderElection.namespace=cert-manager` (not the default `kube-system`) because Autopilot restricts access to managed namespaces.

### Install kmorph with webhooks

```bash
helm upgrade --install kmorph charts/kmorph \
  --namespace kmorph-system \
  --create-namespace \
  --set webhooks.enabled=true \
  --set webhooks.certManager.enabled=true
```

This creates:
- `Issuer/kmorph-selfsigned` — self-signed CA in `kmorph-system`
- `Certificate/kmorph-webhook-tls` — TLS cert signed by the CA
- `Secret/kmorph-webhook-tls` — mounted into the manager pod
- `Service/kmorph-webhook-service` — exposes port 9443
- `ValidatingWebhookConfiguration/kmorph-validating` — with automatic caBundle injection

### How caBundle injection works

cert-manager's `cainjector` watches Certificates with the annotation:
```yaml
cert-manager.io/inject-ca-from: kmorph-system/kmorph-webhook-tls
```

When the Certificate is issued, cainjector automatically injects the CA into the `ValidatingWebhookConfiguration.webhooks[].clientConfig.caBundle` field. No manual steps required.

## Failure policy

By default, webhooks use `failurePolicy: Ignore` — if the webhook server is unavailable, requests are allowed through. This means the cluster continues to work even during kmorph upgrades.

For strict enforcement (reject requests if webhook is unavailable):

```bash
helm upgrade kmorph charts/kmorph \
  --set webhooks.failurePolicy=Fail
```

> Use `Fail` only in production clusters where high availability (2+ replicas) is guaranteed.

## Verify webhooks are working

```bash
# Should be rejected with validation error
kubectl apply -f - <<EOF
apiVersion: config.kmorph.io/v1alpha1
kind: ClusterProfile
metadata:
  name: invalid-test
spec:
  patches:
  - target:
      namespace: my-app
      kind: Deployment
      # missing: name or labelSelector
    patch:
      spec:
        replicas: 1
EOF
# Error: admission webhook "vclusterprofile.kb.io" denied the request:
# spec.patches[0].target: Invalid value: ...either name or labelSelector must be specified
```

## Disable webhooks

```bash
helm upgrade kmorph charts/kmorph \
  --set webhooks.enabled=false
```

## Helm values reference

| Value | Default | Description |
|-------|---------|-------------|
| `webhooks.enabled` | `false` | Enable validating webhooks |
| `webhooks.failurePolicy` | `Ignore` | `Ignore` or `Fail` when webhook is unavailable |
| `webhooks.certManager.enabled` | `false` | Use cert-manager for TLS (requires cert-manager CRDs) |
| `webhooks.certManager.duration` | `8760h` | Certificate validity duration (1 year) |
| `webhooks.certManager.renewBefore` | `360h` | Renew certificate 15 days before expiry |
| `webhooks.caBundle` | `""` | Base64-encoded CA for manual TLS (when certManager.enabled=false) |
