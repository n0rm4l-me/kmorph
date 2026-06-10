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
	runtime "k8s.io/apimachinery/pkg/runtime"
)

// DriftPolicy defines how the controller handles drift from the desired state.
// +kubebuilder:validation:Enum=strict;soft;audit
type DriftPolicy string

const (
	DriftPolicyStrict DriftPolicy = "strict"
	DriftPolicySoft   DriftPolicy = "soft"
	DriftPolicyAudit  DriftPolicy = "audit"
)

// ResourceTarget identifies a set of resources to patch.
type ResourceTarget struct {
	// namespace to target.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// group is the API group, e.g. "apps". Empty string means core group.
	// +optional
	Group string `json:"group,omitempty"`

	// kind of the resource, e.g. "Deployment", "StatefulSet".
	// +kubebuilder:validation:Required
	Kind string `json:"kind"`

	// name targets a specific resource by name. Mutually exclusive with labelSelector.
	// +optional
	Name string `json:"name,omitempty"`

	// labelSelector targets resources matching the given labels.
	// +optional
	LabelSelector map[string]string `json:"labelSelector,omitempty"`
}

// PatchType defines the type of patch to apply.
// +kubebuilder:validation:Enum=strategic;merge
type PatchType string

const (
	PatchTypeStrategic PatchType = "strategic"
	PatchTypeMerge     PatchType = "merge"
)

// RolloutPolicy defines how kmorph interacts with Argo Rollout objects.
// When set, kmorph enters Rollout-aware mode for this patch target.
type RolloutPolicy struct {
	// skipSteps instructs the Rollout controller to skip all canary analysis steps
	// by setting the argoproj.io/skip-steps annotation on the Rollout object itself
	// (not on the pod template). The annotation is removed once the Rollout becomes Healthy.
	// +kubebuilder:default=false
	// +optional
	SkipSteps bool `json:"skipSteps,omitempty"`

	// abortOnFailure automatically reverts the patch and restores the previous
	// stable configuration if the Rollout transitions to Degraded during promote.
	// +kubebuilder:default=true
	// +optional
	AbortOnFailure bool `json:"abortOnFailure,omitempty"`

	// progressDeadlineSeconds is the maximum time in seconds to wait for the
	// Rollout to reach Healthy after patching. If exceeded, the patch is aborted.
	// +kubebuilder:default=600
	// +optional
	ProgressDeadlineSeconds int `json:"progressDeadlineSeconds,omitempty"`
}

// ResourcePatch defines a patch to apply to a set of resources.
type ResourcePatch struct {
	// target identifies which resources to patch.
	// +kubebuilder:validation:Required
	Target ResourceTarget `json:"target"`

	// patch is the patch to apply to the matched resources.
	// +kubebuilder:validation:Required
	// +kubebuilder:pruning:PreserveUnknownFields
	Patch runtime.RawExtension `json:"patch"`

	// patchType defines how the patch is applied.
	// strategic: strategic merge patch (default, works best for native k8s resources).
	// merge: JSON merge patch (use for custom CRDs like Rollout).
	// +kubebuilder:default=strategic
	// +optional
	PatchType PatchType `json:"patchType,omitempty"`

	// rolloutPolicy enables Rollout-aware patching for Argo Rollout targets.
	// When set, kmorph manages the full promote lifecycle instead of blindly patching.
	// Only valid when target.kind=Rollout and target.group=argoproj.io.
	// +optional
	RolloutPolicy *RolloutPolicy `json:"rolloutPolicy,omitempty"`
}

// ClusterProfileSpec defines the desired state of ClusterProfile.
type ClusterProfileSpec struct {
	// driftPolicy defines how the controller handles manual changes when this profile is active.
	// strict: continuously re-applies patches.
	// soft: applies once, reports drift in status.
	// audit: never applies, only reports what would change.
	// +kubebuilder:default=strict
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`

	// patches is the list of patches this profile applies when activated.
	// +kubebuilder:validation:Required
	Patches []ResourcePatch `json:"patches"`
}

// ClusterProfileStatus defines the observed state of ClusterProfile.
type ClusterProfileStatus struct {
	// activations lists ProfileActivations that currently reference this profile.
	// +optional
	Activations []string `json:"activations,omitempty"`

	// conditions represent the current state of the ClusterProfile resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cp
// +kubebuilder:printcolumn:name="Drift Policy",type=string,JSONPath=`.spec.driftPolicy`
// +kubebuilder:printcolumn:name="Activations",type=string,JSONPath=`.status.activations`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ClusterProfile defines a named set of patches to apply to cluster resources.
type ClusterProfile struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec ClusterProfileSpec `json:"spec"`

	// +optional
	Status ClusterProfileStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterProfileList contains a list of ClusterProfile.
type ClusterProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ClusterProfile `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterProfile{}, &ClusterProfileList{})
}
