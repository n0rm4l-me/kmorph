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

package controller

import (
	"encoding/json"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

// --- evaluateSchedule ---

func TestEvaluateSchedule_NilSchedule(t *testing.T) {
	inWindow, next, err := evaluateSchedule(nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inWindow {
		t.Error("nil schedule should always be in-window")
	}
	if next != nil {
		t.Error("nil schedule should return nil nextTransition")
	}
}

func TestEvaluateSchedule_InvalidCron(t *testing.T) {
	sched := &configv1alpha1.ActivationSchedule{
		Start: "not-a-cron",
		End:   "0 18 * * *",
	}
	_, _, err := evaluateSchedule(sched, time.Now())
	if err == nil {
		t.Error("expected error for invalid cron expression")
	}
}

func TestEvaluateSchedule_InvalidTimezone(t *testing.T) {
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 9 * * *",
		End:      "0 18 * * *",
		Timezone: "Mars/Olympus",
	}
	_, _, err := evaluateSchedule(sched, time.Now())
	if err == nil {
		t.Error("expected error for invalid timezone")
	}
}

func TestEvaluateSchedule_InWindow(t *testing.T) {
	// Tuesday 14:00 UTC — inside Mon-Fri 09:00-18:00
	loc, _ := time.LoadLocation("UTC")
	now := time.Date(2026, 6, 10, 14, 0, 0, 0, loc) // Tuesday
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 9 * * 1-5",
		End:      "0 18 * * 1-5",
		Timezone: "UTC",
	}
	inWindow, next, err := evaluateSchedule(sched, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inWindow {
		t.Error("14:00 on Tuesday should be in-window for 09:00-18:00 Mon-Fri")
	}
	if next == nil {
		t.Error("expected nextTransition to be set")
	}
}

func TestEvaluateSchedule_OutOfWindow(t *testing.T) {
	// Tuesday 20:00 UTC — outside Mon-Fri 09:00-18:00
	loc, _ := time.LoadLocation("UTC")
	now := time.Date(2026, 6, 10, 20, 0, 0, 0, loc)
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 9 * * 1-5",
		End:      "0 18 * * 1-5",
		Timezone: "UTC",
	}
	inWindow, next, err := evaluateSchedule(sched, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inWindow {
		t.Error("20:00 on Tuesday should be out-of-window for 09:00-18:00 Mon-Fri")
	}
	if next == nil {
		t.Error("expected nextTransition (next start) to be set")
	}
}

// --- resolveGVK ---

func TestResolveGVK_KnownTypes(t *testing.T) {
	cases := []struct {
		group, kind    string
		wantGroup, wantVersion string
	}{
		{"", "Deployment", "apps", "v1"},
		{"", "StatefulSet", "apps", "v1"},
		{"", "DaemonSet", "apps", "v1"},
		{"", "ConfigMap", "", "v1"},
		{"", "Service", "", "v1"},
		{"argoproj.io", "Rollout", "argoproj.io", "v1alpha1"},
		{"keda.sh", "ScaledObject", "keda.sh", "v1alpha1"},
	}
	for _, c := range cases {
		gvk := resolveGVK(c.group, c.kind)
		if gvk.Group != c.wantGroup {
			t.Errorf("resolveGVK(%q, %q).Group = %q, want %q", c.group, c.kind, gvk.Group, c.wantGroup)
		}
		if gvk.Version != c.wantVersion {
			t.Errorf("resolveGVK(%q, %q).Version = %q, want %q", c.group, c.kind, gvk.Version, c.wantVersion)
		}
		if gvk.Kind != c.kind {
			t.Errorf("resolveGVK(%q, %q).Kind = %q, want %q", c.group, c.kind, gvk.Kind, c.kind)
		}
	}
}

func TestResolveGVK_UnknownCRDFallsBackToV1Alpha1(t *testing.T) {
	gvk := resolveGVK("some.custom.io", "MyResource")
	if gvk.Version != "v1alpha1" {
		t.Errorf("unknown CRD should fall back to v1alpha1, got %q", gvk.Version)
	}
}

// --- patchTypeToK8s ---

func TestPatchTypeToK8s(t *testing.T) {
	cases := []struct {
		pt   configv1alpha1.PatchType
		want types.PatchType
	}{
		{configv1alpha1.PatchTypeStrategic, types.StrategicMergePatchType},
		{configv1alpha1.PatchTypeMerge, types.MergePatchType},
		{configv1alpha1.PatchTypeJSON, types.JSONPatchType},
		{"", types.StrategicMergePatchType}, // default
	}
	for _, c := range cases {
		got := patchTypeToK8s(c.pt)
		if got != c.want {
			t.Errorf("patchTypeToK8s(%q) = %q, want %q", c.pt, got, c.want)
		}
	}
}

// --- mapsSubset ---

func TestMapsSubset(t *testing.T) {
	cases := []struct {
		name   string
		subset map[string]interface{}
		target map[string]interface{}
		want   bool
	}{
		{
			name:   "empty subset always matches",
			subset: map[string]interface{}{},
			target: map[string]interface{}{"a": "1"},
			want:   true,
		},
		{
			name:   "matching key",
			subset: map[string]interface{}{"a": "1"},
			target: map[string]interface{}{"a": "1", "b": "2"},
			want:   true,
		},
		{
			name:   "missing key",
			subset: map[string]interface{}{"c": "3"},
			target: map[string]interface{}{"a": "1"},
			want:   false,
		},
		{
			name:   "nested match",
			subset: map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}},
			target: map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1), "extra": "x"}},
			want:   true,
		},
		{
			name:   "nested mismatch",
			subset: map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(2)}},
			target: map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}},
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mapsSubset(c.subset, c.target)
			if got != c.want {
				t.Errorf("mapsSubset() = %v, want %v", got, c.want)
			}
		})
	}
}

// --- hasDrift ---

func TestHasDrift(t *testing.T) {
	obj := unstructured.Unstructured{
		Object: map[string]interface{}{
			"spec": map[string]interface{}{
				"replicas": float64(1),
			},
		},
	}

	// Patch that matches — no drift.
	matchPatch := mustRawExtension(map[string]interface{}{
		"spec": map[string]interface{}{"replicas": float64(1)},
	})
	if hasDrift(obj, matchPatch) {
		t.Error("no drift expected when patch matches")
	}

	// Patch that differs — drift detected.
	diffPatch := mustRawExtension(map[string]interface{}{
		"spec": map[string]interface{}{"replicas": float64(3)},
	})
	if !hasDrift(obj, diffPatch) {
		t.Error("drift expected when patch differs")
	}

	// Empty patch — no drift.
	if hasDrift(obj, runtime.RawExtension{}) {
		t.Error("no drift expected for empty patch")
	}
}

// --- rolloutProgress helpers ---

func TestFindRolloutProgress_NotFound(t *testing.T) {
	activation := &configv1alpha1.ProfileActivation{}
	p := findRolloutProgress(activation, "ns", "name")
	if p != nil {
		t.Error("expected nil for empty activation")
	}
}

func TestUpsertRolloutProgress(t *testing.T) {
	activation := &configv1alpha1.ProfileActivation{}

	p1 := configv1alpha1.RolloutProgress{Namespace: "ns", Name: "svc", Phase: configv1alpha1.RolloutPhaseProgressing}
	upsertRolloutProgress(activation, p1)
	if len(activation.Status.RolloutProgress) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(activation.Status.RolloutProgress))
	}

	// Update existing entry.
	p2 := configv1alpha1.RolloutProgress{Namespace: "ns", Name: "svc", Phase: configv1alpha1.RolloutPhaseHealthy}
	upsertRolloutProgress(activation, p2)
	if len(activation.Status.RolloutProgress) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(activation.Status.RolloutProgress))
	}
	if activation.Status.RolloutProgress[0].Phase != configv1alpha1.RolloutPhaseHealthy {
		t.Error("expected phase to be updated to Healthy")
	}

	// Add different entry.
	p3 := configv1alpha1.RolloutProgress{Namespace: "ns", Name: "other", Phase: configv1alpha1.RolloutPhaseProgressing}
	upsertRolloutProgress(activation, p3)
	if len(activation.Status.RolloutProgress) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(activation.Status.RolloutProgress))
	}
}

func TestRolloutPhaseFromUnstructured(t *testing.T) {
	cases := []struct {
		phase string
		want  configv1alpha1.RolloutPhase
	}{
		{"Healthy", configv1alpha1.RolloutPhaseHealthy},
		{"Progressing", configv1alpha1.RolloutPhaseProgressing},
		{"Degraded", configv1alpha1.RolloutPhaseDegraded},
		{"Paused", configv1alpha1.RolloutPhasePaused},
		{"", configv1alpha1.RolloutPhaseUnknown},
		{"Whatever", configv1alpha1.RolloutPhaseUnknown},
	}
	for _, c := range cases {
		obj := unstructured.Unstructured{Object: map[string]interface{}{
			"status": map[string]interface{}{"phase": c.phase},
		}}
		got := rolloutPhaseFromUnstructured(obj)
		if got != c.want {
			t.Errorf("rolloutPhaseFromUnstructured(%q) = %q, want %q", c.phase, got, c.want)
		}
	}
}

// --- helpers ---

func mustRawExtension(v interface{}) runtime.RawExtension {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return runtime.RawExtension{Raw: raw}
}
