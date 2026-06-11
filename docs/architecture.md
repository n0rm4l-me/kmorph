# Architecture

## Overview

kmorph consists of two CRDs and two controllers. The **ClusterProfile** defines what to patch. The **ProfileActivation** defines when and with what priority to apply it.

```mermaid
graph TD
    User["👤 User / GitOps"] -->|creates/updates| PA[ProfileActivation]
    User -->|creates| CP[ClusterProfile]

    PA -->|references| CP
    CP -->|contains| Patches["Patches[]<br/>(target + patch + patchType + rolloutPolicy)"]

    subgraph kmorph-system
        PAC[ProfileActivation<br/>Controller]
        CPC[ClusterProfile<br/>Controller]
        WH["Webhook Server<br/>:9443<br/>(optional)"]
    end

    PA --> PAC
    CP --> CPC
    PA -->|validated at admission| WH
    CP -->|validated at admission| WH

    PAC -->|dryRun=false: selects winner| Winner["Winning Activation"]
    PAC -->|dryRun=true: server-side dry-run| DryResult["DryRunResult<br/>(no real changes)"]
    Winner -->|applies patches| K8S["Kubernetes Resources<br/>(Deployment, Rollout, etc.)"]

    PAC -->|emits| Events["Kubernetes Events<br/>(Activated, Preempted, Expired)"]
    PAC -->|exposes| Metrics["Prometheus Metrics<br/>:8080/metrics"]

    subgraph cert-manager
        Issuer["Issuer<br/>(self-signed)"]
        Cert["Certificate<br/>(kmorph-webhook-tls)"]
        CAInject["cainjector"]
    end

    Issuer -->|signs| Cert
    Cert -->|TLS Secret| WH
    CAInject -->|injects caBundle| VWC["ValidatingWebhookConfiguration"]
```

## Reconciliation loop

```mermaid
flowchart TD
    Start([Reconcile triggered]) --> GetActivation[Get ProfileActivation]
    GetActivation --> Deleting{Being deleted?}

    Deleting -->|yes| Cleanup[Run cleanup<br/>remove Rollout annotations<br/>remove finalizer]
    Cleanup --> Done([Done])

    Deleting -->|no| AddFinalizer{Has finalizer?}
    AddFinalizer -->|no| SetFinalizer[Add finalizer<br/>config.kmorph.io/cleanup]
    SetFinalizer --> Requeue([Requeue])

    AddFinalizer -->|yes| DryRun{spec.dryRun?}
    DryRun -->|yes| LoadProfileDR[Load ClusterProfile]
    LoadProfileDR --> RunDryRun[Apply all patches<br/>with dry-run=server]
    RunDryRun --> SaveResult[Save DryRunResult<br/>in status]
    SaveResult --> Requeue60s([Requeue 60s])

    DryRun -->|no| Suspended{Suspended?}
    Suspended -->|yes| SetSuspended[Set phase = Suspended]
    SetSuspended --> Done

    Suspended -->|no| Expired{ExpiresAt in past?}
    Expired -->|yes| SetExpired[Set phase = Expired]
    SetExpired --> Done

    Expired -->|no| Schedule{In schedule window?}
    Schedule -->|no| SetPending[Set phase = Pending<br/>requeue at next window]
    SetPending --> Done

    Schedule -->|yes| FindWinner[List all eligible activations<br/>sort by priority desc]
    FindWinner --> IsWinner{This activation<br/>is the winner?}

    IsWinner -->|no| SetPending2[Set phase = Pending<br/>emit Preempted event]
    SetPending2 --> Done

    IsWinner -->|yes| LoadProfile[Load ClusterProfile]
    LoadProfile --> NotFound{Profile exists?}
    NotFound -->|no| SetPending3[Set phase = Pending<br/>ProfileNotFound]
    SetPending3 --> Requeue30s([Requeue 30s])

    NotFound -->|yes| ApplyPatches[Apply all patches]
    ApplyPatches --> Error{Error?}
    Error -->|yes| SetApplyFailed[Set condition ApplyFailed<br/>emit event]
    SetApplyFailed --> Requeue10s([Requeue 10s])

    Error -->|no| UpdateStatus[Set phase = Active<br/>update LastAppliedTime<br/>track RolloutProgress]
    UpdateStatus --> RequeueStrict([Requeue 30s<br/>strict drift enforcement])
```

## Priority resolution

When multiple ProfileActivations are simultaneously eligible (in-window, not expired, not suspended), the controller selects the winner by:

1. **Highest `spec.priority`** wins
2. **Alphabetical name** breaks ties (deterministic)

```mermaid
graph LR
    A["fallback<br/>priority=0"] -->|preempted by| B
    B["business-hours<br/>priority=10"] -->|preempted by| C
    C["sleep-nights<br/>priority=20"] -->|preempted by| D
    D["client-demo<br/>priority=100<br/>duration=4h"] -->|ACTIVE| K8S["Kubernetes<br/>Resources"]

    style D fill:#22c55e,color:#fff
    style A fill:#94a3b8,color:#fff
    style B fill:#94a3b8,color:#fff
    style C fill:#94a3b8,color:#fff
```

## Argo Rollout lifecycle

When `rolloutPolicy` is set, kmorph manages the full promote lifecycle:

```mermaid
sequenceDiagram
    participant kmorph
    participant Rollout
    participant ReplicaSet

    kmorph->>Rollout: Set annotation argoproj.io/skip-steps=true
    kmorph->>Rollout: Apply patches (replicas, resources, nodeSelector)
    Rollout->>ReplicaSet: Create new ReplicaSet
    kmorph->>Rollout: Call promoteFull (status.promoteFull=true)
    Rollout-->>kmorph: phase=Progressing
    Note over kmorph: Watch phase every 10s
    Rollout-->>kmorph: phase=Healthy
    kmorph->>Rollout: Remove argoproj.io/skip-steps annotation
    kmorph->>kmorph: Record RolloutProgress.completedAt

    alt phase=Degraded or timeout
        kmorph->>Rollout: abort (status.abort=true)
        kmorph->>Rollout: revert to previousStableRS
        kmorph->>kmorph: emit RolloutFailed event
    end
```

## Drift enforcement

In `strict` mode, kmorph re-applies patches every 30 seconds. For standard resources (Deployment, StatefulSet), it compares `spec.replicas` and other patched fields using recursive map subset check.

For Rollout resources, only `spec.replicas` is checked for drift — template fields are intentionally excluded to avoid triggering spurious canary revisions.

```mermaid
flowchart LR
    Reconcile([Reconcile every 30s]) --> Check{Drift detected?}
    Check -->|no| Skip([Skip])
    Check -->|yes, strict| Patch[Re-apply patch]
    Check -->|yes, soft| Report[Update status.driftDetected]
    Check -->|yes, audit| ReportOnly[Update status.driftDetected<br/>no patch applied]
```

## Data flow

```mermaid
graph LR
    subgraph API Server
        CP_API[ClusterProfile]
        PA_API[ProfileActivation]
        Dep[Deployment / Rollout / ...]
        VWC[ValidatingWebhookConfiguration]
    end

    subgraph kmorph Pod
        Cache[Informer Cache]
        PAR[ProfileActivation<br/>Reconciler]
        CPR[ClusterProfile<br/>Reconciler]
        WHS[Webhook Server<br/>:9443]
        Metrics[Prometheus<br/>:8080/metrics]
    end

    subgraph cert-manager
        TLS[TLS Secret]
    end

    CP_API --> Cache
    PA_API --> Cache
    Dep --> Cache

    Cache --> PAR
    Cache --> CPR

    CP_API -->|"change → immediate reconcile"| PAR

    PAR -->|patch / dry-run| Dep
    PAR -->|status update| PA_API
    CPR -->|status update| CP_API

    PAR --> Metrics

    VWC -->|admission request| WHS
    TLS -->|mount| WHS
    WHS -->|allow/deny| VWC
```

## CRD structure

```mermaid
classDiagram
    class ClusterProfile {
        +string name
        +DriftPolicy driftPolicy
        +ResourcePatch[] patches
        --
        +string[] activations
        +Condition[] conditions
    }

    class ResourcePatch {
        +ResourceTarget target
        +RawExtension patch
        +PatchType patchType
        +RolloutPolicy rolloutPolicy
    }

    class ResourceTarget {
        +string namespace
        +string group
        +string kind
        +string name
        +map labelSelector
    }

    class RolloutPolicy {
        +bool skipSteps
        +bool abortOnFailure
        +int progressDeadlineSeconds
    }

    class ProfileActivation {
        +string profileRef
        +int32 priority
        +ActivationSchedule schedule
        +string duration
        +bool suspended
        +bool dryRun
        --
        +ActivationPhase phase
        +string activeProfile
        +Time activeSince
        +Time expiresAt
        +DriftEntry[] driftDetected
        +RolloutProgress[] rolloutProgress
        +DryRunResult dryRunResult
    }

    class ActivationSchedule {
        +string start
        +string end
        +string timezone
    }

    ClusterProfile "1" --> "*" ResourcePatch
    ResourcePatch --> ResourceTarget
    ResourcePatch --> RolloutPolicy
    ProfileActivation --> ActivationSchedule
    ProfileActivation "*" --> "1" ClusterProfile : profileRef
```
