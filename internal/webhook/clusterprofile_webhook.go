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

package webhook

import (
	"context"
	"fmt"

	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

type ClusterProfileValidator struct {
	client.Client
}

func (v *ClusterProfileValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &configv1alpha1.ClusterProfile{}).
		WithValidator(v).
		Complete()
}

func (v *ClusterProfileValidator) ValidateCreate(_ context.Context, obj *configv1alpha1.ClusterProfile) (admission.Warnings, error) {
	return nil, validateClusterProfile(obj)
}

func (v *ClusterProfileValidator) ValidateUpdate(_ context.Context, _, newObj *configv1alpha1.ClusterProfile) (admission.Warnings, error) {
	return nil, validateClusterProfile(newObj)
}

func (v *ClusterProfileValidator) ValidateDelete(ctx context.Context, obj *configv1alpha1.ClusterProfile) (admission.Warnings, error) {
	activationList := &configv1alpha1.ProfileActivationList{}
	if err := v.List(ctx, activationList); err != nil {
		return nil, err
	}
	for _, a := range activationList.Items {
		if a.Spec.ProfileRef == obj.Name && a.Status.Phase == configv1alpha1.ActivationPhaseActive {
			return nil, fmt.Errorf("cannot delete ClusterProfile %q: ProfileActivation %q is currently active",
				obj.Name, a.Name)
		}
	}
	return nil, nil
}

func validateClusterProfile(profile *configv1alpha1.ClusterProfile) error {
	var errs field.ErrorList

	if len(profile.Spec.Patches) == 0 {
		errs = append(errs, field.Required(
			field.NewPath("spec", "patches"),
			"at least one patch is required",
		))
	}

	for i, p := range profile.Spec.Patches {
		if p.Target.Namespace == "" {
			errs = append(errs, field.Required(
				field.NewPath("spec", "patches").Index(i).Child("target", "namespace"),
				"namespace is required",
			))
		}
		if p.Target.Kind == "" {
			errs = append(errs, field.Required(
				field.NewPath("spec", "patches").Index(i).Child("target", "kind"),
				"kind is required",
			))
		}
		if p.Target.Name == "" && len(p.Target.LabelSelector) == 0 {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "patches").Index(i).Child("target"),
				p.Target,
				"either name or labelSelector must be specified",
			))
		}
		if len(p.Patch.Raw) == 0 {
			errs = append(errs, field.Required(
				field.NewPath("spec", "patches").Index(i).Child("patch"),
				"patch must not be empty",
			))
		}
	}

	if len(errs) > 0 {
		return errs.ToAggregate()
	}
	return nil
}

// validateCron is a shared helper used by both webhooks.
func validateCron(expr string) (cron.Schedule, error) {
	return cron.ParseStandard(expr)
}
