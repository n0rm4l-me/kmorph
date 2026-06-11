/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ActivationPhase describes the current phase of a ProfileActivation.
// +kubebuilder:validation:Enum=Pending;Active;Expired;Suspended;DryRun
type ActivationPhase string

const (
	ActivationPhasePending   ActivationPhase = "Pending"
	ActivationPhaseActive    ActivationPhase = "Active"
	ActivationPhaseExpired   ActivationPhase = "Expired"
	ActivationPhaseSuspended ActivationPhase = "Suspended"
	ActivationPhaseDryRun    ActivationPhase = "DryRun"
)

// ActivationSchedule defines a recurring time window using cron expressions.
type ActivationSchedule struct {
	// start is a cron expression for when this activation becomes active.
	// e.g. "0 9 * * 1-5" — weekdays at 09:00
	// +kubebuilder:validation:Required
	Start string `json:"start"`

	// end is a cron expression for when this activation stops being active.
	// e.g. "0 18 * * 1-5" — weekdays at 18:00
	// +kubebuilder:validation:Required
	End string `json:"end"`

	// timezone for interpreting the cron expressions. Defaults to UTC.
	// e.g. "Asia/Tokyo"
	// +kubebuilder:default="UTC"
	// +optional
	Timezone string `json:"timezone,omitempty"`
}

// ProfileActivationSpec defines the desired state of ProfileActivation.
type ProfileActivationSpec struct {
	// profileRef is the name of the ClusterProfile to activate.
	// +kubebuilder:validation:Required
	ProfileRef string `json:"profileRef"`

	// priority determines which activation wins when multiple are active simultaneously.
	// Higher value wins. Default is 0.
	// +kubebuilder:default=0
	// +optional
	Priority int32 `json:"priority,omitempty"`

	// schedule defines a recurring time window for this activation.
	// If omitted, the activation is always considered active (until expired or suspended).
	// +optional
	Schedule *ActivationSchedule `json:"schedule,omitempty"`

	// duration defines how long this activation stays active after it first becomes active.
	// After the duration expires, the activation moves to Expired phase.
	// Format: Go duration string, e.g. "4h", "30m", "24h".
	// If omitted, the activation does not expire automatically.
	// +optional
	Duration string `json:"duration,omitempty"`

	// suspended pauses this activation without deleting it.
	// While suspended, this activation is ignored by the controller.
	// +kubebuilder:default=false
	// +optional
	Suspended bool `json:"suspended,omitempty"`

	// dryRun enables preview mode. When true, kmorph applies patches with
	// server-side dry-run and records the diff in status.dryRunResult without
	// making any real changes to cluster resources.
	// DryRun activations do not participate in priority election — they always
	// run independently and never preempt real activations.
	// +kubebuilder:default=false
	// +optional
	DryRun bool `json:"dryRun,omitempty"`
}

// RolloutPhase mirrors Argo Rollout phase values.
// +kubebuilder:validation:Enum=Progressing;Healthy;Degraded;Aborted;Paused;Unknown
type RolloutPhase string

const (
	RolloutPhaseProgressing RolloutPhase = "Progressing"
	RolloutPhaseHealthy     RolloutPhase = "Healthy"
	RolloutPhaseDegraded    RolloutPhase = "Degraded"
	RolloutPhaseAborted     RolloutPhase = "Aborted"
	RolloutPhasePaused      RolloutPhase = "Paused"
	RolloutPhaseUnknown     RolloutPhase = "Unknown"
)

// RolloutProgress tracks the promote lifecycle for a single Rollout target.
type RolloutProgress struct {
	// namespace of the Rollout.
	Namespace string `json:"namespace"`
	// name of the Rollout.
	Name string `json:"name"`
	// phase is the current Rollout phase observed by kmorph.
	Phase RolloutPhase `json:"phase"`
	// previousStableRS is the ReplicaSet hash before kmorph applied the patch.
	// Used to revert if abortOnFailure triggers.
	// +optional
	PreviousStableRS string `json:"previousStableRS,omitempty"`
	// patchedAt is when kmorph applied the patch.
	// +optional
	PatchedAt *metav1.Time `json:"patchedAt,omitempty"`
	// completedAt is when the Rollout reached Healthy after patching.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// message describes the current state or failure reason.
	// +optional
	Message string `json:"message,omitempty"`
}

// DryRunChange describes what would change for a single resource in dry-run mode.
type DryRunChange struct {
	// namespace of the resource.
	Namespace string `json:"namespace"`
	// kind of the resource.
	Kind string `json:"kind"`
	// name of the resource.
	Name string `json:"name"`
	// changed is true if the patch would modify this resource.
	Changed bool `json:"changed"`
	// diff is a human-readable summary of the fields that would change.
	// +optional
	Diff string `json:"diff,omitempty"`
}

// DryRunResult summarises what would happen if this activation were applied for real.
type DryRunResult struct {
	// evaluatedAt is when the dry-run was last evaluated.
	EvaluatedAt metav1.Time `json:"evaluatedAt"`
	// profile is the ClusterProfile that was evaluated.
	Profile string `json:"profile"`
	// changes lists per-resource results.
	// +optional
	Changes []DryRunChange `json:"changes,omitempty"`
	// summary is a human-readable description, e.g. "3 resources would change".
	// +optional
	Summary string `json:"summary,omitempty"`
}

// DriftEntry records a single drifted resource.
type DriftEntry struct {
	// namespace of the drifted resource.
	Namespace string `json:"namespace"`
	// kind of the drifted resource.
	Kind string `json:"kind"`
	// name of the drifted resource.
	Name string `json:"name"`
	// detectedAt is when the drift was detected.
	DetectedAt metav1.Time `json:"detectedAt"`
}

// ProfileActivationStatus defines the observed state of ProfileActivation.
type ProfileActivationStatus struct {
	// phase is the current lifecycle phase of this activation.
	// +optional
	Phase ActivationPhase `json:"phase,omitempty"`

	// activeProfile is the name of the ClusterProfile currently being applied.
	// +optional
	ActiveProfile string `json:"activeProfile,omitempty"`

	// activeSince is when this activation last became Active.
	// +optional
	ActiveSince *metav1.Time `json:"activeSince,omitempty"`

	// expiresAt is when this activation will expire (set when duration is specified).
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// nextTransition is the estimated time of the next schedule-driven state change.
	// +optional
	NextTransition *metav1.Time `json:"nextTransition,omitempty"`

	// lastAppliedTime is when patches were last applied.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// driftDetected lists resources that have drifted from the desired state (soft/audit modes).
	// +optional
	DriftDetected []DriftEntry `json:"driftDetected,omitempty"`

	// rolloutProgress tracks in-flight Rollout promote operations managed by kmorph.
	// +optional
	RolloutProgress []RolloutProgress `json:"rolloutProgress,omitempty"`

	// dryRunResult contains the result of the last dry-run evaluation.
	// Only set when spec.dryRun=true.
	// +optional
	DryRunResult *DryRunResult `json:"dryRunResult,omitempty"`

	// conditions represent the current state of the ProfileActivation resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=pa
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.spec.profileRef`
// +kubebuilder:printcolumn:name="Priority",type=integer,JSONPath=`.spec.priority`
// +kubebuilder:printcolumn:name="Dry Run",type=boolean,JSONPath=`.spec.dryRun`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Active Since",type=date,JSONPath=`.status.activeSince`
// +kubebuilder:printcolumn:name="Expires At",type=date,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ProfileActivation activates a ClusterProfile, optionally on a schedule or for a fixed duration.
// When multiple ProfileActivations are active simultaneously, the one with the highest priority wins.
type ProfileActivation struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec ProfileActivationSpec `json:"spec"`

	// +optional
	Status ProfileActivationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ProfileActivationList contains a list of ProfileActivation.
type ProfileActivationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ProfileActivation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProfileActivation{}, &ProfileActivationList{})
}
