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
	"reflect"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

// evaluateDryRun applies all patches in the ClusterProfile using server-side dry-run
// and returns a DryRunResult describing what would change without modifying anything.
func (r *ProfileActivationReconciler) evaluateDryRun(
	ctx context.Context,
	profile *configv1alpha1.ClusterProfile,
) (*configv1alpha1.DryRunResult, error) {
	result := &configv1alpha1.DryRunResult{
		EvaluatedAt: metav1.Now(),
		Profile:     profile.Name,
	}

	for _, rp := range profile.Spec.Patches {
		if rp.RolloutPolicy != nil {
			// Skip Rollout patches in dry-run — promote lifecycle is too complex to simulate.
			result.Changes = append(result.Changes, configv1alpha1.DryRunChange{
				Namespace: rp.Target.Namespace,
				Kind:      rp.Target.Kind,
				Name:      rp.Target.Name,
				Changed:   false,
				Diff:      "(Rollout patches skipped in dry-run mode)",
			})
			continue
		}

		resources, err := r.listTargetResources(ctx, rp.Target)
		if err != nil {
			return nil, fmt.Errorf("listing targets: %w", err)
		}

		patchData := rp.Patch.Raw
		if len(patchData) == 0 {
			continue
		}

		for _, res := range resources {
			change, err := r.dryRunPatch(ctx, res, rp, patchData)
			if err != nil {
				// Non-fatal — record the error as a change entry.
				result.Changes = append(result.Changes, configv1alpha1.DryRunChange{
					Namespace: res.GetNamespace(),
					Kind:      res.GetKind(),
					Name:      res.GetName(),
					Changed:   false,
					Diff:      fmt.Sprintf("dry-run error: %v", err),
				})
				continue
			}
			result.Changes = append(result.Changes, *change)
		}
	}

	// Build summary.
	changed := 0
	for _, c := range result.Changes {
		if c.Changed {
			changed++
		}
	}
	if changed == 0 {
		result.Summary = fmt.Sprintf("no changes — %d resource(s) already match profile", len(result.Changes))
	} else {
		result.Summary = fmt.Sprintf("%d of %d resource(s) would change", changed, len(result.Changes))
	}

	return result, nil
}

// dryRunPatch applies a single patch with dry-run=server and returns the diff.
func (r *ProfileActivationReconciler) dryRunPatch(
	ctx context.Context,
	current unstructured.Unstructured,
	rp configv1alpha1.ResourcePatch,
	patchData []byte,
) (*configv1alpha1.DryRunChange, error) {
	// Clone to apply dry-run against.
	simulated := current.DeepCopy()

	pt := patchTypeToK8s(rp.PatchType)
	if err := r.Patch(ctx, simulated, client.RawPatch(pt, patchData),
		client.DryRunAll,
	); err != nil {
		return nil, err
	}

	// Compare current vs simulated to build diff.
	diff := diffObjects(current.Object, simulated.Object, "")
	changed := diff != ""

	change := &configv1alpha1.DryRunChange{
		Namespace: current.GetNamespace(),
		Kind:      current.GetKind(),
		Name:      current.GetName(),
		Changed:   changed,
	}
	if changed {
		change.Diff = diff
	}
	return change, nil
}

// diffObjects returns a human-readable diff of changed fields between two unstructured objects.
// Only reports fields present in `next` that differ from `current`.
func diffObjects(current, next map[string]interface{}, path string) string {
	var lines []string
	for k, nextVal := range next {
		fullPath := k
		if path != "" {
			fullPath = path + "." + k
		}
		currentVal, exists := current[k]
		if !exists {
			lines = append(lines, fmt.Sprintf("+ %s: %s", fullPath, formatVal(nextVal)))
			continue
		}
		nextMap, nextIsMap := nextVal.(map[string]interface{})
		currentMap, currentIsMap := currentVal.(map[string]interface{})
		if nextIsMap && currentIsMap {
			if nested := diffObjects(currentMap, nextMap, fullPath); nested != "" {
				lines = append(lines, nested)
			}
			continue
		}
		if !reflect.DeepEqual(currentVal, nextVal) {
			lines = append(lines, fmt.Sprintf("~ %s: %s → %s", fullPath, formatVal(currentVal), formatVal(nextVal)))
		}
	}
	return strings.Join(lines, "\n")
}

func formatVal(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		return fmt.Sprintf("%t", val)
	case nil:
		return "<nil>"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		s := string(b)
		if len(s) > 80 {
			return s[:77] + "..."
		}
		return s
	}
}

// handleDryRunActivation runs dry-run evaluation and updates status.
// Returns requeue duration (re-evaluate every 60s).
func (r *ProfileActivationReconciler) handleDryRunActivation(
	ctx context.Context,
	activation *configv1alpha1.ProfileActivation,
	profile *configv1alpha1.ClusterProfile,
) error {
	log := logFromContext(ctx)

	dryRunResult, err := r.evaluateDryRun(ctx, profile)
	if err != nil {
		log.Error(err, "dry-run evaluation failed")
		return err
	}

	patch := client.MergeFrom(activation.DeepCopy())
	activation.Status.Phase = configv1alpha1.ActivationPhaseDryRun
	activation.Status.ActiveProfile = profile.Name
	activation.Status.DryRunResult = dryRunResult
	setActivationCondition(activation, "Ready", metav1.ConditionTrue, "DryRunComplete",
		dryRunResult.Summary)

	log.Info("dry-run complete", "profile", profile.Name, "summary", dryRunResult.Summary)
	return r.Status().Patch(ctx, activation, patch)
}

