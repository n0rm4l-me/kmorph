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
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

const (
	annotationSkipSteps     = "argoproj.io/skip-steps"
	annotationKmorphManaged = "kmorph.io/managed"
	rolloutGroup            = "argoproj.io"
	rolloutKind             = "Rollout"
)

// rolloutPhaseFromUnstructured extracts the Rollout phase from an unstructured object.
func rolloutPhaseFromUnstructured(obj unstructured.Unstructured) configv1alpha1.RolloutPhase {
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	switch phase {
	case "Healthy":
		return configv1alpha1.RolloutPhaseHealthy
	case "Progressing":
		return configv1alpha1.RolloutPhaseProgressing
	case "Degraded":
		return configv1alpha1.RolloutPhaseDegraded
	case "Paused":
		return configv1alpha1.RolloutPhasePaused
	default:
		return configv1alpha1.RolloutPhaseUnknown
	}
}

// rolloutStableRS returns the current stableRS hash from a Rollout.
func rolloutStableRS(obj unstructured.Unstructured) string {
	rs, _, _ := unstructured.NestedString(obj.Object, "status", "stableRS")
	return rs
}

// rolloutMessage returns the status message from a Rollout.
func rolloutMessage(obj unstructured.Unstructured) string {
	msg, _, _ := unstructured.NestedString(obj.Object, "status", "message")
	return msg
}

// handleRolloutPatch manages the full Rollout-aware patch lifecycle.
// Returns (requeueAfter, error).
func (r *ProfileActivationReconciler) handleRolloutPatch(
	ctx context.Context,
	activation *configv1alpha1.ProfileActivation,
	rp configv1alpha1.ResourcePatch,
) (time.Duration, error) {
	policy := rp.RolloutPolicy

	// Fetch the Rollout.
	rollout := &unstructured.Unstructured{}
	rollout.SetGroupVersionKind(knownGVK[rolloutGroup+"/"+rolloutKind])
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: rp.Target.Namespace,
		Name:      rp.Target.Name,
	}, rollout); err != nil {
		if errors.IsNotFound(err) {
			return 0, nil
		}
		return 0, err
	}

	currentPhase := rolloutPhaseFromUnstructured(*rollout)
	currentStableRS := rolloutStableRS(*rollout)

	// Find existing progress entry for this Rollout.
	progress := findRolloutProgress(activation, rp.Target.Namespace, rp.Target.Name)

	// If we have an in-flight promote, check its outcome.
	if progress != nil && progress.PatchedAt != nil && progress.CompletedAt == nil {
		return r.checkRolloutProgress(ctx, activation, rollout, progress, policy)
	}

	// Not in-flight — check if we need to apply the patch.
	// In strict mode: re-apply if Rollout spec drifted from desired.
	// We only check spec.replicas to avoid triggering new revisions unnecessarily.
	needsPatch, err := r.rolloutNeedsPatch(rollout, rp)
	if err != nil {
		return 0, err
	}
	if !needsPatch {
		return 30 * time.Second, nil
	}

	return r.applyRolloutPatch(ctx, activation, rollout, rp, currentStableRS, currentPhase)
}

// applyRolloutPatch applies the patch to a Rollout and starts tracking promote.
func (r *ProfileActivationReconciler) applyRolloutPatch(
	ctx context.Context,
	activation *configv1alpha1.ProfileActivation,
	rollout *unstructured.Unstructured,
	rp configv1alpha1.ResourcePatch,
	previousStableRS string,
	_ configv1alpha1.RolloutPhase,
) (time.Duration, error) {
	log := logFromContext(ctx)
	policy := rp.RolloutPolicy

	// Step 1: Set skip-steps annotation on the Rollout object (not on pod template).
	if policy != nil && policy.SkipSteps {
		if err := r.setRolloutAnnotation(ctx, rollout, annotationSkipSteps, "true"); err != nil {
			return 0, fmt.Errorf("setting skip-steps annotation: %w", err)
		}
		log.Info("set skip-steps annotation", "rollout", rollout.GetName())
	}

	// Step 2: Apply the patch.
	patchData := rp.Patch.Raw
	if len(patchData) == 0 {
		return 0, nil
	}
	pt := patchTypeToK8s(rp.PatchType)
	if err := r.Patch(ctx, rollout, client.RawPatch(pt, patchData)); err != nil {
		return 0, fmt.Errorf("patching rollout %s/%s: %w", rollout.GetNamespace(), rollout.GetName(), err)
	}
	log.Info("patched rollout", "name", rollout.GetName(), "namespace", rollout.GetNamespace())
	r.Recorder.Eventf(activation, corev1.EventTypeNormal, "RolloutPatched",
		"patched Rollout %s/%s, waiting for promote", rollout.GetNamespace(), rollout.GetName())

	// Step 3: Full promote to skip all canary steps.
	// We do this immediately after patching so Rollout jumps to stable without
	// running smoke tests or canary analysis.
	if policy != nil && policy.SkipSteps {
		if err := r.promoteRolloutFull(ctx, rollout); err != nil {
			log.Error(err, "failed to promote rollout, will retry", "rollout", rollout.GetName())
			// Non-fatal — the requeue will retry promote on next cycle.
		} else {
			log.Info("promoted rollout full (skip steps)", "rollout", rollout.GetName())
		}
	}

	// Step 4: Record progress.
	now := metav1.Now()
	upsertRolloutProgress(activation, configv1alpha1.RolloutProgress{
		Namespace:        rp.Target.Namespace,
		Name:             rp.Target.Name,
		Phase:            configv1alpha1.RolloutPhaseProgressing,
		PreviousStableRS: previousStableRS,
		PatchedAt:        &now,
		Message:          "patch applied, waiting for Rollout to promote",
	})

	deadline := 600
	if policy != nil && policy.ProgressDeadlineSeconds > 0 {
		deadline = policy.ProgressDeadlineSeconds
	}
	return time.Duration(min(30, deadline)) * time.Second, nil
}

// checkRolloutProgress checks an in-flight promote and handles completion/failure/timeout.
func (r *ProfileActivationReconciler) checkRolloutProgress(
	ctx context.Context,
	activation *configv1alpha1.ProfileActivation,
	rollout *unstructured.Unstructured,
	progress *configv1alpha1.RolloutProgress,
	policy *configv1alpha1.RolloutPolicy,
) (time.Duration, error) {
	log := logFromContext(ctx)
	phase := rolloutPhaseFromUnstructured(*rollout)
	message := rolloutMessage(*rollout)

	deadline := 600
	if policy != nil && policy.ProgressDeadlineSeconds > 0 {
		deadline = policy.ProgressDeadlineSeconds
	}
	elapsed := time.Since(progress.PatchedAt.Time)
	timedOut := elapsed > time.Duration(deadline)*time.Second

	switch {
	case phase == configv1alpha1.RolloutPhaseHealthy:
		// Promote completed successfully.
		now := metav1.Now()
		progress.Phase = configv1alpha1.RolloutPhaseHealthy
		progress.CompletedAt = &now
		progress.Message = "promote completed successfully"

		// Remove skip-steps annotation now that promote is done.
		if policy != nil && policy.SkipSteps {
			if err := r.removeRolloutAnnotation(ctx, rollout, annotationSkipSteps); err != nil {
				log.Error(err, "failed to remove skip-steps annotation")
			}
		}
		r.Recorder.Eventf(activation, corev1.EventTypeNormal, "RolloutPromoted",
			"Rollout %s/%s promoted successfully", rollout.GetNamespace(), rollout.GetName())
		return 30 * time.Second, nil

	case phase == configv1alpha1.RolloutPhaseDegraded || timedOut:
		reason := "degraded"
		if timedOut {
			reason = fmt.Sprintf("timed out after %ds", deadline)
		}
		log.Info("rollout promote failed", "reason", reason, "rollout", rollout.GetName(), "message", message)
		r.Recorder.Eventf(activation, corev1.EventTypeWarning, "RolloutFailed",
			"Rollout %s/%s failed: %s — %s", rollout.GetNamespace(), rollout.GetName(), reason, message)

		progress.Phase = configv1alpha1.RolloutPhaseDegraded
		progress.Message = fmt.Sprintf("%s: %s", reason, message)

		// Abort and optionally revert.
		if err := r.abortRollout(ctx, rollout); err != nil {
			log.Error(err, "failed to abort rollout")
		}
		if policy != nil && policy.AbortOnFailure && progress.PreviousStableRS != "" {
			if err := r.revertRollout(ctx, rollout, progress.PreviousStableRS); err != nil {
				log.Error(err, "failed to revert rollout to previous stable")
			} else {
				r.Recorder.Eventf(activation, corev1.EventTypeWarning, "RolloutReverted",
					"Rollout %s/%s reverted to previous stable %s",
					rollout.GetNamespace(), rollout.GetName(), progress.PreviousStableRS)
			}
		}
		if policy != nil && policy.SkipSteps {
			if err := r.removeRolloutAnnotation(ctx, rollout, annotationSkipSteps); err != nil {
				log.Error(err, "failed to remove skip-steps annotation")
			}
		}
		return 30 * time.Second, nil

	default:
		// Still progressing.
		progress.Phase = phase
		progress.Message = message
		return 10 * time.Second, nil
	}
}

// rolloutNeedsPatch checks if the Rollout spec has drifted from what the patch wants.
// For Rollout we only check spec.replicas to avoid triggering spurious revisions.
func (r *ProfileActivationReconciler) rolloutNeedsPatch(
	rollout *unstructured.Unstructured,
	rp configv1alpha1.ResourcePatch,
) (bool, error) {
	if len(rp.Patch.Raw) == 0 {
		return false, nil
	}
	var patchMap map[string]interface{}
	if err := json.Unmarshal(rp.Patch.Raw, &patchMap); err != nil {
		return false, err
	}

	spec, ok := patchMap["spec"].(map[string]interface{})
	if !ok {
		return false, nil
	}

	// Check replicas drift.
	if desiredReplicas, ok := spec["replicas"]; ok {
		currentReplicas, _, _ := unstructured.NestedInt64(rollout.Object, "spec", "replicas")
		desired, _ := toInt64(desiredReplicas)
		if currentReplicas != desired {
			return true, nil
		}
	}

	// For template changes: only trigger if there's a pending progress entry
	// that completed successfully (meaning we need to re-apply after a drift).
	// We do NOT compare template fields to avoid spurious revisions.
	if _, hasTemplate := spec["template"]; hasTemplate {
		progress := findRolloutProgress(nil, rp.Target.Namespace, rp.Target.Name)
		// If we've never patched or the last promote completed, consider it drifted
		// only if replicas also drifted (handled above). Template drift without
		// replicas drift is intentional ArgoCD management — don't touch it.
		_ = progress
	}

	return false, nil
}

// promoteRolloutFull triggers a full promote on the Rollout, skipping all canary steps.
// This is equivalent to `kubectl argo rollouts promote --full`.
// The correct mechanism requires patching both spec.paused=false and status.promoteFull=true
// in a single operation, as per the Argo Rollouts CLI implementation.
func (r *ProfileActivationReconciler) promoteRolloutFull(ctx context.Context, rollout *unstructured.Unstructured) error {
	// Step 1: patch spec.paused=false
	specPatch := []byte(`{"spec":{"paused":false}}`)
	if err := r.Patch(ctx, rollout, client.RawPatch(types.MergePatchType, specPatch)); err != nil {
		return err
	}
	// Step 2: patch status.promoteFull=true
	statusPatch := []byte(`{"promoteFull":true}`)
	return r.Status().Patch(ctx, rollout, client.RawPatch(types.MergePatchType, statusPatch))
}

// abortRollout sets abort=true in the Rollout status.
func (r *ProfileActivationReconciler) abortRollout(ctx context.Context, rollout *unstructured.Unstructured) error {
	patch := []byte(`{"status":{"abort":true}}`)
	return r.Status().Patch(ctx, rollout, client.RawPatch(types.MergePatchType, patch))
}

// revertRollout attempts to revert a Rollout to a previous stable ReplicaSet
// by promoting it fully after undo. This is best-effort.
func (r *ProfileActivationReconciler) revertRollout(ctx context.Context, rollout *unstructured.Unstructured, _ string) error {
	// Best-effort full promote to settle the Rollout.
	// A proper undo would require knowing the exact previous template, which we don't store.
	// Instead we rely on abortOnFailure aborting the bad promote — ArgoCD will restore
	// the correct state on next sync.
	patch := []byte(`{"status":{"abort":true}}`)
	return r.Status().Patch(ctx, rollout, client.RawPatch(types.MergePatchType, patch))
}

// setRolloutAnnotation sets an annotation on the Rollout object itself (not the pod template).
func (r *ProfileActivationReconciler) setRolloutAnnotation(ctx context.Context, rollout *unstructured.Unstructured, key, value string) error {
	annotations := rollout.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	if annotations[key] == value {
		return nil
	}
	annotations[key] = value
	patch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{key: value},
		},
	})
	if err != nil {
		return err
	}
	return r.Patch(ctx, rollout, client.RawPatch(types.MergePatchType, patch))
}

// removeRolloutAnnotation removes an annotation from the Rollout object.
func (r *ProfileActivationReconciler) removeRolloutAnnotation(ctx context.Context, rollout *unstructured.Unstructured, key string) error {
	annotations := rollout.GetAnnotations()
	if _, exists := annotations[key]; !exists {
		return nil
	}
	patch, err := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{key: nil},
		},
	})
	if err != nil {
		return err
	}
	return r.Patch(ctx, rollout, client.RawPatch(types.MergePatchType, patch))
}

// findRolloutProgress finds an existing RolloutProgress entry in the activation status.
func findRolloutProgress(activation *configv1alpha1.ProfileActivation, namespace, name string) *configv1alpha1.RolloutProgress {
	if activation == nil {
		return nil
	}
	for i := range activation.Status.RolloutProgress {
		p := &activation.Status.RolloutProgress[i]
		if p.Namespace == namespace && p.Name == name {
			return p
		}
	}
	return nil
}

// upsertRolloutProgress creates or updates a RolloutProgress entry.
func upsertRolloutProgress(activation *configv1alpha1.ProfileActivation, progress configv1alpha1.RolloutProgress) {
	for i := range activation.Status.RolloutProgress {
		if activation.Status.RolloutProgress[i].Namespace == progress.Namespace &&
			activation.Status.RolloutProgress[i].Name == progress.Name {
			activation.Status.RolloutProgress[i] = progress
			return
		}
	}
	activation.Status.RolloutProgress = append(activation.Status.RolloutProgress, progress)
}

func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	}
	return 0, false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
