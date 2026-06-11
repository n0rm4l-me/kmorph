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
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
	kmorphmetrics "github.com/n0rm4l-me/kmorph/internal/metrics"
)

const finalizerName = "config.kmorph.io/cleanup"

// ProfileActivationReconciler reconciles ProfileActivation objects.
// It selects the highest-priority active activation, loads its ClusterProfile,
// and applies the patches to the target resources.
type ProfileActivationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=config.kmorph.io,resources=profileactivations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.kmorph.io,resources=profileactivations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.kmorph.io,resources=profileactivations/finalizers,verbs=update
// +kubebuilder:rbac:groups=config.kmorph.io,resources=clusterprofiles,verbs=get;list;watch
// +kubebuilder:rbac:groups="*",resources="*",verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ProfileActivationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	activation := &configv1alpha1.ProfileActivation{}
	if err := r.Get(ctx, req.NamespacedName, activation); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion — run cleanup logic before allowing deletion.
	if !activation.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, activation)
	}

	// Ensure finalizer is present.
	if !containsString(activation.Finalizers, finalizerName) {
		patch := client.MergeFrom(activation.DeepCopy())
		activation.Finalizers = append(activation.Finalizers, finalizerName)
		if err := r.Patch(ctx, activation, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	now := time.Now()

	// Handle suspended activations.
	if activation.Spec.Suspended {
		if activation.Status.Phase != configv1alpha1.ActivationPhaseSuspended {
			r.Recorder.Event(activation, corev1.EventTypeNormal, "Suspended", "activation suspended by user")
		}
		return ctrl.Result{}, r.setPhase(ctx, activation, configv1alpha1.ActivationPhaseSuspended, "Suspended", "activation is suspended")
	}

	// Check duration expiry.
	if activation.Status.ExpiresAt != nil && now.After(activation.Status.ExpiresAt.Time) {
		if activation.Status.Phase != configv1alpha1.ActivationPhaseExpired {
			r.Recorder.Eventf(activation, corev1.EventTypeNormal, "Expired", "activation expired after duration %s", activation.Spec.Duration)
		}
		return ctrl.Result{}, r.setPhase(ctx, activation, configv1alpha1.ActivationPhaseExpired, "Expired", "duration has elapsed")
	}

	// Evaluate schedule.
	inWindow, nextTransition, err := evaluateSchedule(activation.Spec.Schedule, now)
	if err != nil {
		log.Error(err, "invalid schedule")
		return ctrl.Result{}, r.setPhase(ctx, activation, configv1alpha1.ActivationPhasePending, "InvalidSchedule", err.Error())
	}

	if !inWindow {
		patch := client.MergeFrom(activation.DeepCopy())
		activation.Status.Phase = configv1alpha1.ActivationPhasePending
		if nextTransition != nil {
			t := metav1.NewTime(*nextTransition)
			activation.Status.NextTransition = &t
		}
		if err := r.Status().Patch(ctx, activation, patch); err != nil {
			return ctrl.Result{}, err
		}
		if nextTransition != nil {
			return ctrl.Result{RequeueAfter: time.Until(*nextTransition) + time.Second}, nil
		}
		return ctrl.Result{}, nil
	}

	// We are in window — find the winner among all active activations.
	winner, requeueAfter, err := r.findWinner(ctx, now)
	if err != nil {
		return ctrl.Result{}, err
	}

	// If this activation is not the winner, mark it Pending and requeue.
	if winner == nil || winner.Name != activation.Name {
		if activation.Status.Phase == configv1alpha1.ActivationPhaseActive {
			winnerName := ""
			if winner != nil {
				winnerName = winner.Name
			}
			r.Recorder.Eventf(activation, corev1.EventTypeNormal, "Preempted",
				"preempted by higher-priority activation %q (priority %d)", winnerName, winner.Spec.Priority)
			kmorphmetrics.ActivationSwitchTotal.WithLabelValues(activation.Spec.ProfileRef, "preempted").Inc()
		}
		kmorphmetrics.PendingActivations.Inc()
		patch := client.MergeFrom(activation.DeepCopy())
		activation.Status.Phase = configv1alpha1.ActivationPhasePending
		if err := r.Status().Patch(ctx, activation, patch); err != nil {
			return ctrl.Result{}, err
		}
		if requeueAfter > 0 {
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// This activation is the winner — load and apply the profile.
	profile := &configv1alpha1.ClusterProfile{}
	if err := r.Get(ctx, types.NamespacedName{Name: activation.Spec.ProfileRef}, profile); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, r.setPhase(ctx, activation, configv1alpha1.ActivationPhasePending, "ProfileNotFound",
				fmt.Sprintf("ClusterProfile %q not found", activation.Spec.ProfileRef))
		}
		return ctrl.Result{}, err
	}

	drifts, rolloutRequeue, err := r.applyProfile(ctx, activation, profile)
	if err != nil {
		log.Error(err, "failed to apply profile", "profile", profile.Name)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, r.setPhase(ctx, activation, configv1alpha1.ActivationPhaseActive, "ApplyFailed", err.Error())
	}

	// Emit event when first becoming active or when profile changes.
	wasActive := activation.Status.Phase == configv1alpha1.ActivationPhaseActive
	if !wasActive {
		r.Recorder.Eventf(activation, corev1.EventTypeNormal, "Activated",
			"profile %q activated (priority %d)", profile.Name, activation.Spec.Priority)
		kmorphmetrics.ActiveActivations.Inc()
		kmorphmetrics.ActivationSwitchTotal.WithLabelValues(profile.Name, "activated").Inc()
	}
	if len(drifts) > 0 {
		if profile.Spec.DriftPolicy != configv1alpha1.DriftPolicyStrict {
			r.Recorder.Eventf(activation, corev1.EventTypeWarning, "DriftDetected",
				"%d resource(s) drifted from profile %q", len(drifts), profile.Name)
		}
		kmorphmetrics.DriftEventsTotal.WithLabelValues(profile.Name, string(profile.Spec.DriftPolicy)).Add(float64(len(drifts)))
	}

	// Update status.
	now2 := metav1.Now()
	statusPatch := client.MergeFrom(activation.DeepCopy())
	activation.Status.Phase = configv1alpha1.ActivationPhaseActive
	activation.Status.ActiveProfile = profile.Name
	activation.Status.LastAppliedTime = &now2
	activation.Status.DriftDetected = drifts
	// Reset ActiveSince on each transition into Active so it reflects the current activation window.
	if !wasActive {
		activation.Status.ActiveSince = &now2
	}
	if nextTransition != nil {
		t := metav1.NewTime(*nextTransition)
		activation.Status.NextTransition = &t
	}
	// Set duration expiry on first activation.
	if activation.Spec.Duration != "" && activation.Status.ExpiresAt == nil {
		d, err := time.ParseDuration(activation.Spec.Duration)
		if err == nil {
			exp := metav1.NewTime(now2.Add(d))
			activation.Status.ExpiresAt = &exp
		}
	}
	setActivationCondition(activation, "Ready", metav1.ConditionTrue, "Applied",
		fmt.Sprintf("profile %q applied successfully", profile.Name))
	if err := r.Status().Patch(ctx, activation, statusPatch); err != nil {
		return ctrl.Result{}, err
	}

	// Requeue based on drift policy, schedule, or in-flight Rollout promotes.
	requeue := 30 * time.Second
	if rolloutRequeue > 0 && rolloutRequeue < requeue {
		requeue = rolloutRequeue
	}
	if nextTransition != nil {
		untilNext := time.Until(*nextTransition)
		if untilNext > 0 && untilNext < requeue {
			requeue = untilNext + time.Second
		}
	}
	if activation.Status.ExpiresAt != nil {
		untilExpiry := time.Until(activation.Status.ExpiresAt.Time)
		if untilExpiry > 0 && untilExpiry < requeue {
			requeue = untilExpiry + time.Second
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// findWinner lists all ProfileActivations that are currently in-window and not suspended/expired,
// and returns the one with the highest priority.
func (r *ProfileActivationReconciler) findWinner(ctx context.Context, now time.Time) (*configv1alpha1.ProfileActivation, time.Duration, error) {
	list := &configv1alpha1.ProfileActivationList{}
	if err := r.List(ctx, list); err != nil {
		return nil, 0, err
	}

	var candidates []configv1alpha1.ProfileActivation
	for _, a := range list.Items {
		if a.Spec.Suspended {
			continue
		}
		if a.Status.ExpiresAt != nil && now.After(a.Status.ExpiresAt.Time) {
			continue
		}
		inWindow, _, err := evaluateSchedule(a.Spec.Schedule, now)
		if err != nil || !inWindow {
			continue
		}
		candidates = append(candidates, a)
	}

	if len(candidates) == 0 {
		return nil, 0, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Spec.Priority != candidates[j].Spec.Priority {
			return candidates[i].Spec.Priority > candidates[j].Spec.Priority
		}
		return candidates[i].Name < candidates[j].Name
	})

	winner := candidates[0]
	return &winner, 0, nil
}

// applyProfile applies all patches from the ClusterProfile.
// Returns drift entries (soft/audit) and the shortest requeue duration from Rollout handlers.
func (r *ProfileActivationReconciler) applyProfile(
	ctx context.Context,
	activation *configv1alpha1.ProfileActivation,
	profile *configv1alpha1.ClusterProfile,
) ([]configv1alpha1.DriftEntry, time.Duration, error) {
	log := logf.FromContext(ctx)
	var drifts []configv1alpha1.DriftEntry
	var patched []string // tracks successfully patched resources for error context
	minRequeue := 30 * time.Second

	// Collect Rollout targets that need promoteFull after all patches applied.
	// Key: "namespace/name", Value: RolloutPolicy
	type rolloutPromoteTarget struct {
		namespace string
		name      string
		policy    *configv1alpha1.RolloutPolicy
	}
	var rolloutPromotes []rolloutPromoteTarget
	seenRollouts := map[string]bool{}

	for _, rp := range profile.Spec.Patches {
		// Route Rollout patches with rolloutPolicy through the Rollout-aware handler.
		if rp.Target.Group == rolloutGroup && rp.Target.Kind == rolloutKind && rp.RolloutPolicy != nil {
			requeue, err := r.handleRolloutPatch(ctx, activation, rp)
			if err != nil {
				return nil, 0, fmt.Errorf("rollout patch %s/%s: %w", rp.Target.Namespace, rp.Target.Name, err)
			}
			if requeue > 0 && requeue < minRequeue {
				minRequeue = requeue
			}
			key := rp.Target.Namespace + "/" + rp.Target.Name
			if !seenRollouts[key] && rp.RolloutPolicy.SkipSteps {
				rolloutPromotes = append(rolloutPromotes, rolloutPromoteTarget{
					namespace: rp.Target.Namespace,
					name:      rp.Target.Name,
					policy:    rp.RolloutPolicy,
				})
				seenRollouts[key] = true
			}
			continue
		}

		// For Rollout targets WITHOUT rolloutPolicy — apply directly but track for promote.
		if rp.Target.Group == rolloutGroup && rp.Target.Kind == rolloutKind {
			key := rp.Target.Namespace + "/" + rp.Target.Name
			if !seenRollouts[key] {
				// Check if another patch in this profile has rolloutPolicy.skipSteps for same target.
				for _, other := range profile.Spec.Patches {
					if other.Target.Namespace == rp.Target.Namespace &&
						other.Target.Name == rp.Target.Name &&
						other.RolloutPolicy != nil && other.RolloutPolicy.SkipSteps {
						seenRollouts[key] = true
						break
					}
				}
			}
		}

		// Standard patch path.
		resources, err := r.listTargetResources(ctx, rp.Target)
		if err != nil {
			return nil, 0, fmt.Errorf("listing targets for %s/%s: %w", rp.Target.Namespace, rp.Target.Kind, err)
		}

		patchData := rp.Patch.Raw
		if len(patchData) == 0 {
			continue
		}

		for i := range resources {
			res := resources[i]
			if profile.Spec.DriftPolicy == configv1alpha1.DriftPolicyAudit {
				if hasDrift(res, rp.Patch) {
					drifts = append(drifts, configv1alpha1.DriftEntry{
						Namespace:  res.GetNamespace(),
						Kind:       res.GetKind(),
						Name:       res.GetName(),
						DetectedAt: metav1.Now(),
					})
				}
				continue
			}

			if profile.Spec.DriftPolicy == configv1alpha1.DriftPolicySoft && !hasDrift(res, rp.Patch) {
				continue
			}

			pt := patchTypeToK8s(rp.PatchType)
			if err := r.Patch(ctx, &res, client.RawPatch(pt, patchData)); err != nil {
				log.Error(err, "failed to patch resource",
					"kind", res.GetKind(), "name", res.GetName(), "namespace", res.GetNamespace(),
					"patchedSoFar", len(patched))
				kmorphmetrics.PatchFailedTotal.WithLabelValues(profile.Name, res.GetNamespace(), res.GetKind()).Inc()
				return nil, 0, fmt.Errorf("patch %s/%s/%s failed: %w (already patched %d resource(s))",
					res.GetNamespace(), res.GetKind(), res.GetName(), err, len(patched))
			}
			patched = append(patched, res.GetNamespace()+"/"+res.GetKind()+"/"+res.GetName())
			kmorphmetrics.PatchAppliedTotal.WithLabelValues(profile.Name, res.GetNamespace(), res.GetKind(), string(rp.PatchType)).Inc()
			log.Info("patched resource", "kind", res.GetKind(), "name", res.GetName(), "namespace", res.GetNamespace())
		}
	}
	// After all patches applied — promote Rollouts that have skipSteps=true.
	// This must happen AFTER all template changes to avoid triggering canary mid-patch.
	for _, target := range rolloutPromotes {
		rollout := &unstructured.Unstructured{}
		rollout.SetGroupVersionKind(knownGVK[rolloutGroup+"/"+rolloutKind])
		if err := r.Get(ctx, types.NamespacedName{Namespace: target.namespace, Name: target.name}, rollout); err != nil {
			if !errors.IsNotFound(err) {
				log.Error(err, "failed to get rollout for promote", "name", target.name)
			}
			continue
		}
		if err := r.promoteRolloutFull(ctx, rollout); err != nil {
			log.Error(err, "failed to promote rollout full", "name", target.name)
		} else {
			log.Info("promoted rollout full (skip steps)", "rollout", target.name, "namespace", target.namespace)
		}
	}

	return drifts, minRequeue, nil
}

// listTargetResources returns matching unstructured resources for the given target.
func (r *ProfileActivationReconciler) listTargetResources(ctx context.Context, target configv1alpha1.ResourceTarget) ([]unstructured.Unstructured, error) {
	gvk := resolveGVK(target.Group, target.Kind)

	if target.Name != "" {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(gvk)
		if err := r.Get(ctx, types.NamespacedName{Namespace: target.Namespace, Name: target.Name}, obj); err != nil {
			if errors.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		return []unstructured.Unstructured{*obj}, nil
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   gvk.Group,
		Version: gvk.Version,
		Kind:    gvk.Kind + "List",
	})
	opts := []client.ListOption{client.InNamespace(target.Namespace)}
	if len(target.LabelSelector) > 0 {
		opts = append(opts, client.MatchingLabels(target.LabelSelector))
	}
	if err := r.List(ctx, list, opts...); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (r *ProfileActivationReconciler) setPhase(ctx context.Context, activation *configv1alpha1.ProfileActivation, phase configv1alpha1.ActivationPhase, reason, message string) error {
	patch := client.MergeFrom(activation.DeepCopy())
	activation.Status.Phase = phase
	setActivationCondition(activation, "Ready", metav1.ConditionFalse, reason, message)
	return r.Status().Patch(ctx, activation, patch)
}

// evaluateSchedule returns whether we are currently inside the schedule window,
// and the time of the next transition (start or end).
func evaluateSchedule(schedule *configv1alpha1.ActivationSchedule, now time.Time) (bool, *time.Time, error) {
	if schedule == nil {
		return true, nil, nil
	}

	loc := time.UTC
	if schedule.Timezone != "" {
		var err error
		loc, err = time.LoadLocation(schedule.Timezone)
		if err != nil {
			return false, nil, fmt.Errorf("invalid timezone %q: %w", schedule.Timezone, err)
		}
	}

	nowInLoc := now.In(loc)

	startSched, err := cron.ParseStandard(schedule.Start)
	if err != nil {
		return false, nil, fmt.Errorf("invalid start cron %q: %w", schedule.Start, err)
	}
	endSched, err := cron.ParseStandard(schedule.End)
	if err != nil {
		return false, nil, fmt.Errorf("invalid end cron %q: %w", schedule.End, err)
	}

	// Find the most recent start and end times before now.
	lastStart := prevScheduleTime(startSched, nowInLoc)
	lastEnd := prevScheduleTime(endSched, nowInLoc)

	inWindow := lastStart.After(lastEnd)

	var nextTransition time.Time
	if inWindow {
		// Next transition is the next end.
		nextTransition = endSched.Next(nowInLoc)
	} else {
		// Next transition is the next start.
		nextTransition = startSched.Next(nowInLoc)
	}

	return inWindow, &nextTransition, nil
}

// prevScheduleTime returns the most recent time before t that matches the schedule.
// We approximate by stepping back in 1-minute increments up to 7 days.
func prevScheduleTime(s cron.Schedule, t time.Time) time.Time {
	// Walk forward from 7 days ago to find the last firing before t.
	check := t.Add(-7 * 24 * time.Hour)
	var last time.Time
	for {
		next := s.Next(check)
		if next.After(t) {
			break
		}
		last = next
		check = next
	}
	return last
}

// knownGVK maps well-known group+kind pairs to their correct version.
var knownGVK = map[string]schema.GroupVersionKind{
	"/Deployment":               appsv1.SchemeGroupVersion.WithKind("Deployment"),
	"/StatefulSet":               appsv1.SchemeGroupVersion.WithKind("StatefulSet"),
	"/DaemonSet":                 appsv1.SchemeGroupVersion.WithKind("DaemonSet"),
	"/ConfigMap":                 corev1.SchemeGroupVersion.WithKind("ConfigMap"),
	"/Service":                   corev1.SchemeGroupVersion.WithKind("Service"),
	"argoproj.io/Rollout":        {Group: "argoproj.io", Version: "v1alpha1", Kind: "Rollout"},
	"argoproj.io/AnalysisRun":    {Group: "argoproj.io", Version: "v1alpha1", Kind: "AnalysisRun"},
	"keda.sh/ScaledObject":       {Group: "keda.sh", Version: "v1alpha1", Kind: "ScaledObject"},
	"keda.sh/ScaledJob":          {Group: "keda.sh", Version: "v1alpha1", Kind: "ScaledJob"},
}

// patchTypeToK8s converts kmorph PatchType to the k8s types.PatchType.
func patchTypeToK8s(pt configv1alpha1.PatchType) types.PatchType {
	switch pt {
	case configv1alpha1.PatchTypeMerge:
		return types.MergePatchType
	case configv1alpha1.PatchTypeJSON:
		return types.JSONPatchType
	default:
		return types.StrategicMergePatchType
	}
}

// resolveGVK maps kind to its GroupVersionKind.
// If group is specified, looks up the known version first, then falls back to v1alpha1.
func resolveGVK(group, kind string) schema.GroupVersionKind {
	key := group + "/" + kind
	if gvk, ok := knownGVK[key]; ok {
		return gvk
	}
	if group == "" {
		// core group — default to v1
		return schema.GroupVersionKind{Group: "", Version: "v1", Kind: kind}
	}
	// unknown custom CRD — try v1alpha1 as the most common version
	return schema.GroupVersionKind{Group: group, Version: "v1alpha1", Kind: kind}
}

// hasDrift checks if the resource's fields differ from what the patch would set.
func hasDrift(res unstructured.Unstructured, patch runtime.RawExtension) bool {
	if len(patch.Raw) == 0 {
		return false
	}
	var patchMap map[string]interface{}
	if err := json.Unmarshal(patch.Raw, &patchMap); err != nil {
		return false
	}
	return !mapsSubset(patchMap, res.Object)
}

// mapsSubset returns true if all keys in subset are present and equal in target (recursive).
func mapsSubset(subset, target map[string]interface{}) bool {
	for k, sv := range subset {
		tv, exists := target[k]
		if !exists {
			return false
		}
		svMap, svIsMap := sv.(map[string]interface{})
		tvMap, tvIsMap := tv.(map[string]interface{})
		if svIsMap && tvIsMap {
			if !mapsSubset(svMap, tvMap) {
				return false
			}
		} else if !svIsMap && !tvIsMap {
			// Compare scalar values using fmt representation for simplicity.
			if fmt.Sprintf("%v", sv) != fmt.Sprintf("%v", tv) {
				return false
			}
		} else {
			// One is a map, the other is not — definitely differs.
			return false
		}
	}
	return true
}

func setActivationCondition(activation *configv1alpha1.ProfileActivation, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i, c := range activation.Status.Conditions {
		if c.Type == condType {
			activation.Status.Conditions[i].Status = status
			activation.Status.Conditions[i].Reason = reason
			activation.Status.Conditions[i].Message = message
			activation.Status.Conditions[i].LastTransitionTime = now
			return
		}
	}
	activation.Status.Conditions = append(activation.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}

// handleDeletion runs cleanup logic when a ProfileActivation is being deleted.
// It removes the finalizer after ensuring Rollout annotations are cleaned up.
func (r *ProfileActivationReconciler) handleDeletion(ctx context.Context, activation *configv1alpha1.ProfileActivation) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !containsString(activation.Finalizers, finalizerName) {
		return ctrl.Result{}, nil
	}

	// Clean up any in-flight Rollout operations — remove skip-steps annotations.
	for _, p := range activation.Status.RolloutProgress {
		if p.CompletedAt != nil {
			continue
		}
		rollout := &unstructured.Unstructured{}
		rollout.SetGroupVersionKind(knownGVK[rolloutGroup+"/"+rolloutKind])
		if err := r.Get(ctx, types.NamespacedName{Namespace: p.Namespace, Name: p.Name}, rollout); err == nil {
			if err := r.removeRolloutAnnotation(ctx, rollout, annotationSkipSteps); err != nil {
				log.Error(err, "cleanup: failed to remove skip-steps annotation", "rollout", p.Name)
			}
		}
	}

	r.Recorder.Eventf(activation, corev1.EventTypeNormal, "Deleted", "activation deleted, cleanup complete")

	// Remove finalizer.
	patch := client.MergeFrom(activation.DeepCopy())
	activation.Finalizers = removeString(activation.Finalizers, finalizerName)
	return ctrl.Result{}, r.Patch(ctx, activation, patch)
}

func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, v := range slice {
		if v != s {
			result = append(result, v)
		}
	}
	return result
}

// logFromContext is a helper to get a logger from context.
func logFromContext(ctx context.Context) logr.Logger {
	return logf.FromContext(ctx)
}

// SetupWithManager sets up the controller with the Manager.
// It also watches Argo Rollout objects so that status changes (Progressing → Healthy/Degraded)
// trigger reconciliation of the owning ProfileActivation without waiting for the requeue timer.
func (r *ProfileActivationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	rolloutGVK := knownGVK[rolloutGroup+"/"+rolloutKind]

	rolloutObj := &unstructured.Unstructured{}
	rolloutObj.SetGroupVersionKind(rolloutGVK)

	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.ProfileActivation{}).
		// Watch Rollout status changes and map them to all ProfileActivations
		// that have an in-flight promote for that Rollout.
		Watches(rolloutObj, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []reconcile.Request {
				return r.rolloutToActivationRequests(ctx, obj)
			},
		)).
		Named("profileactivation").
		Complete(r)
}

// rolloutToActivationRequests maps a Rollout change to the ProfileActivation(s) tracking it.
func (r *ProfileActivationReconciler) rolloutToActivationRequests(ctx context.Context, obj client.Object) []reconcile.Request {
	list := &configv1alpha1.ProfileActivationList{}
	if err := r.List(ctx, list); err != nil {
		return nil
	}
	var requests []reconcile.Request
	for _, a := range list.Items {
		if a.Status.Phase != configv1alpha1.ActivationPhaseActive {
			continue
		}
		for _, p := range a.Status.RolloutProgress {
			if p.Namespace == obj.GetNamespace() && p.Name == obj.GetName() && p.CompletedAt == nil {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: a.Name},
				})
				break
			}
		}
	}
	return requests
}
