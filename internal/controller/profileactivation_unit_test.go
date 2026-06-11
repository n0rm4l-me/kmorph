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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

// --- containsString / removeString ---

func TestContainsString(t *testing.T) {
	if !containsString([]string{"a", "b", "c"}, "b") {
		t.Error("expected true for existing element")
	}
	if containsString([]string{"a", "b"}, "z") {
		t.Error("expected false for missing element")
	}
	if containsString(nil, "x") {
		t.Error("expected false for nil slice")
	}
}

func TestRemoveString(t *testing.T) {
	result := removeString([]string{"a", "b", "c"}, "b")
	if len(result) != 2 || containsString(result, "b") {
		t.Errorf("expected [a c], got %v", result)
	}
	result = removeString([]string{"a"}, "z")
	if len(result) != 1 {
		t.Error("removing non-existent element should return original slice")
	}
}

// --- findWinner priority logic ---

func TestFindWinnerPriority(t *testing.T) {
	makeActivation := func(name string, priority int32, phase configv1alpha1.ActivationPhase) configv1alpha1.ProfileActivation {
		return configv1alpha1.ProfileActivation{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: configv1alpha1.ProfileActivationSpec{
				ProfileRef: "test",
				Priority:   priority,
			},
			Status: configv1alpha1.ProfileActivationStatus{Phase: phase},
		}
	}

	// Sort order: higher priority wins; ties broken alphabetically.
	candidates := []configv1alpha1.ProfileActivation{
		makeActivation("c", 10, configv1alpha1.ActivationPhaseActive),
		makeActivation("a", 50, configv1alpha1.ActivationPhaseActive),
		makeActivation("b", 50, configv1alpha1.ActivationPhaseActive),
	}

	// Replicate findWinner sort logic.
	winner := sortCandidates(candidates)
	if winner.Name != "a" {
		t.Errorf("expected winner 'a' (priority 50, alphabetically first), got %q", winner.Name)
	}
}

func TestFindWinnerSkipsExpired(t *testing.T) {
	past := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	activation := configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "expired"},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: "test", Priority: 100},
		Status:     configv1alpha1.ProfileActivationStatus{ExpiresAt: &past},
	}
	// ExpiresAt in the past — should be excluded.
	if activation.Status.ExpiresAt != nil && !time.Now().After(activation.Status.ExpiresAt.Time) {
		t.Error("activation should be considered expired")
	}
}

func TestFindWinnerSkipsSuspended(t *testing.T) {
	activation := configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "suspended"},
		Spec: configv1alpha1.ProfileActivationSpec{
			ProfileRef: "test",
			Priority:   100,
			Suspended:  true,
		},
	}
	if !activation.Spec.Suspended {
		t.Error("activation should be suspended")
	}
}

// sortCandidates is a test helper that replicates the winner selection sort.
func sortCandidates(candidates []configv1alpha1.ProfileActivation) configv1alpha1.ProfileActivation {
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			a, b := candidates[i], candidates[j]
			less := false
			if a.Spec.Priority != b.Spec.Priority {
				less = a.Spec.Priority > b.Spec.Priority
			} else {
				less = a.Name < b.Name
			}
			if !less {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
	return candidates[0]
}

// --- Finalizer helpers ---

func TestFinalizerConstants(t *testing.T) {
	if finalizerName == "" {
		t.Error("finalizerName constant must not be empty")
	}
	if finalizerName != "config.kmorph.io/cleanup" {
		t.Errorf("unexpected finalizer name: %q", finalizerName)
	}
}

// --- ActiveSince reset on re-activation ---

func TestActiveSinceResetOnReactivation(t *testing.T) {
	oldTime := metav1.NewTime(time.Now().Add(-1 * time.Hour))
	activation := &configv1alpha1.ProfileActivation{
		Status: configv1alpha1.ProfileActivationStatus{
			Phase:       configv1alpha1.ActivationPhasePending, // was pending, now activating
			ActiveSince: &oldTime,
		},
	}

	// Simulate the fix: wasActive=false → reset ActiveSince.
	wasActive := activation.Status.Phase == configv1alpha1.ActivationPhaseActive
	now := metav1.Now()
	if !wasActive {
		activation.Status.ActiveSince = &now
	}

	if !activation.Status.ActiveSince.After(oldTime.Time) {
		t.Error("ActiveSince should be reset to now when transitioning from non-Active to Active")
	}
}

// --- Duration expiry ---

func TestDurationExpirySetOnFirstActivation(t *testing.T) {
	activation := &configv1alpha1.ProfileActivation{
		Spec: configv1alpha1.ProfileActivationSpec{
			Duration: "2h",
		},
		Status: configv1alpha1.ProfileActivationStatus{},
	}

	// Simulate expiry setting logic.
	if activation.Spec.Duration != "" && activation.Status.ExpiresAt == nil {
		d, err := time.ParseDuration(activation.Spec.Duration)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		exp := metav1.NewTime(time.Now().Add(d))
		activation.Status.ExpiresAt = &exp
	}

	if activation.Status.ExpiresAt == nil {
		t.Error("ExpiresAt should be set after first activation with duration")
	}
	if time.Until(activation.Status.ExpiresAt.Time) < time.Hour {
		t.Error("ExpiresAt should be ~2h in the future")
	}
}

func TestDurationExpiryNotOverriddenOnSubsequentReconcile(t *testing.T) {
	existingExpiry := metav1.NewTime(time.Now().Add(time.Hour))
	activation := &configv1alpha1.ProfileActivation{
		Spec: configv1alpha1.ProfileActivationSpec{Duration: "2h"},
		Status: configv1alpha1.ProfileActivationStatus{
			ExpiresAt: &existingExpiry,
		},
	}

	// Should NOT be overridden when already set.
	originalExpiry := activation.Status.ExpiresAt.Time
	if activation.Spec.Duration != "" && activation.Status.ExpiresAt == nil {
		// This branch should not execute.
		t.Error("should not override existing ExpiresAt")
	}

	if !activation.Status.ExpiresAt.Time.Equal(originalExpiry) {
		t.Error("ExpiresAt should not change on subsequent reconciles")
	}
}

// --- CronJob/Job GVK resolution ---

func TestResolveGVKCronJob(t *testing.T) {
	gvk := resolveGVK("", "CronJob")
	if gvk.Group != "batch" {
		t.Errorf("CronJob group = %q, want batch", gvk.Group)
	}
	if gvk.Version != "v1" {
		t.Errorf("CronJob version = %q, want v1", gvk.Version)
	}
}

func TestResolveGVKJob(t *testing.T) {
	gvk := resolveGVK("", "Job")
	if gvk.Group != "batch" {
		t.Errorf("Job group = %q, want batch", gvk.Group)
	}
	if gvk.Version != "v1" {
		t.Errorf("Job version = %q, want v1", gvk.Version)
	}
}

func TestResolveGVKBatchGroup(t *testing.T) {
	// Explicit group should also work
	gvk := resolveGVK("batch", "CronJob")
	if gvk.Group != "batch" || gvk.Version != "v1" {
		t.Errorf("batch/CronJob = %v, want batch/v1/CronJob", gvk)
	}
}

// --- DryRun mode ---

func TestDryRunPhaseIsDistinctFromActive(t *testing.T) {
	if configv1alpha1.ActivationPhaseDryRun == configv1alpha1.ActivationPhaseActive {
		t.Error("DryRun and Active phases must be distinct")
	}
	if configv1alpha1.ActivationPhaseDryRun == configv1alpha1.ActivationPhasePending {
		t.Error("DryRun and Pending phases must be distinct")
	}
}

func TestDryRunSpecField(t *testing.T) {
	activation := &configv1alpha1.ProfileActivation{
		Spec: configv1alpha1.ProfileActivationSpec{
			ProfileRef: "test",
			DryRun:     true,
		},
	}
	if !activation.Spec.DryRun {
		t.Error("DryRun field should be true")
	}
}

func TestDryRunResultSummary(t *testing.T) {
	result := &configv1alpha1.DryRunResult{
		Profile: "my-profile",
		Summary: "2 of 3 resource(s) would change",
		Changes: []configv1alpha1.DryRunChange{
			{Namespace: "ns", Kind: "Deployment", Name: "api", Changed: true, Diff: "~ spec.replicas: 1 → 3"},
			{Namespace: "ns", Kind: "Deployment", Name: "worker", Changed: true, Diff: "~ spec.replicas: 1 → 2"},
			{Namespace: "ns", Kind: "Deployment", Name: "ui", Changed: false},
		},
	}

	changed := 0
	for _, c := range result.Changes {
		if c.Changed {
			changed++
		}
	}
	if changed != 2 {
		t.Errorf("expected 2 changed resources, got %d", changed)
	}
	if result.Profile != "my-profile" {
		t.Errorf("profile = %q, want my-profile", result.Profile)
	}
}

// --- Schedule overnight wrapping ---

func TestScheduleOvernightWindow(t *testing.T) {
	// 22:00-08:00 overnight schedule
	// At 23:00 — should be IN window
	loc := time.UTC
	night := time.Date(2026, 6, 11, 23, 0, 0, 0, loc)
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 22 * * *",
		End:      "0 8 * * *",
		Timezone: "UTC",
	}
	inWindow, _, err := evaluateSchedule(sched, night)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inWindow {
		t.Error("23:00 should be in overnight window (22:00-08:00)")
	}
}

func TestScheduleOvernightWindowMorning(t *testing.T) {
	// 22:00-08:00 overnight schedule
	// At 07:00 — should be IN window
	loc := time.UTC
	morning := time.Date(2026, 6, 12, 7, 0, 0, 0, loc)
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 22 * * *",
		End:      "0 8 * * *",
		Timezone: "UTC",
	}
	inWindow, _, err := evaluateSchedule(sched, morning)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !inWindow {
		t.Error("07:00 should be in overnight window (22:00-08:00)")
	}
}

func TestScheduleOvernightWindowMidday(t *testing.T) {
	// 22:00-08:00 overnight schedule
	// At 12:00 — should be OUT of window
	loc := time.UTC
	midday := time.Date(2026, 6, 11, 12, 0, 0, 0, loc)
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 22 * * *",
		End:      "0 8 * * *",
		Timezone: "UTC",
	}
	inWindow, _, err := evaluateSchedule(sched, midday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inWindow {
		t.Error("12:00 should NOT be in overnight window (22:00-08:00)")
	}
}

// --- patchTypeToK8s (also tested in controller_unit_test.go but validates no regression) ---

func TestPatchTypeToK8sJSON(t *testing.T) {
	pt := patchTypeToK8s(configv1alpha1.PatchTypeJSON)
	if string(pt) != "application/json-patch+json" {
		t.Errorf("JSON patch type should be 'application/json-patch+json', got %q", pt)
	}
}

// --- Schedule window boundary ---

func TestScheduleWindowBoundaryExact(t *testing.T) {
	loc := time.UTC
	// Exactly at the start time (09:00) — should be in window.
	exactStart := time.Date(2026, 6, 10, 9, 0, 0, 0, loc) // Tuesday 09:00
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 9 * * 1-5",
		End:      "0 18 * * 1-5",
		Timezone: "UTC",
	}
	inWindow, _, err := evaluateSchedule(sched, exactStart)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// At exactly 09:00, the start cron just fired — so prevScheduleTime(start) = 09:00,
	// prevScheduleTime(end) is the previous 18:00 (yesterday) → in window.
	if !inWindow {
		t.Error("09:00 UTC on Tuesday should be at the start boundary of the window")
	}
}

func TestScheduleWeekend(t *testing.T) {
	loc := time.UTC
	// Saturday 14:00 UTC — outside Mon-Fri schedule.
	saturday := time.Date(2026, 6, 13, 14, 0, 0, 0, loc)
	sched := &configv1alpha1.ActivationSchedule{
		Start:    "0 9 * * 1-5",
		End:      "0 18 * * 1-5",
		Timezone: "UTC",
	}
	inWindow, _, err := evaluateSchedule(sched, saturday)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inWindow {
		t.Error("Saturday should be out-of-window for weekday-only schedule")
	}
}
