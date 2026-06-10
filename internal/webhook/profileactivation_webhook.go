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
	"time"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

type ProfileActivationValidator struct {
	client.Client
}

func (v *ProfileActivationValidator) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &configv1alpha1.ProfileActivation{}).
		WithValidator(v).
		Complete()
}

func (v *ProfileActivationValidator) ValidateCreate(ctx context.Context, obj *configv1alpha1.ProfileActivation) (admission.Warnings, error) {
	return nil, v.validate(ctx, obj)
}

func (v *ProfileActivationValidator) ValidateUpdate(ctx context.Context, _, newObj *configv1alpha1.ProfileActivation) (admission.Warnings, error) {
	return nil, v.validate(ctx, newObj)
}

func (v *ProfileActivationValidator) ValidateDelete(_ context.Context, _ *configv1alpha1.ProfileActivation) (admission.Warnings, error) {
	return nil, nil
}

func (v *ProfileActivationValidator) validate(ctx context.Context, activation *configv1alpha1.ProfileActivation) error {
	var errs field.ErrorList

	// profileRef must reference an existing ClusterProfile.
	profile := &configv1alpha1.ClusterProfile{}
	if err := v.Get(ctx, types.NamespacedName{Name: activation.Spec.ProfileRef}, profile); err != nil {
		errs = append(errs, field.Invalid(
			field.NewPath("spec", "profileRef"),
			activation.Spec.ProfileRef,
			fmt.Sprintf("ClusterProfile %q not found", activation.Spec.ProfileRef),
		))
	}

	// duration must be a valid Go duration string.
	if activation.Spec.Duration != "" {
		if _, err := time.ParseDuration(activation.Spec.Duration); err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "duration"),
				activation.Spec.Duration,
				fmt.Sprintf("invalid duration: %v", err),
			))
		}
	}

	// schedule cron expressions must be valid.
	if activation.Spec.Schedule != nil {
		if _, err := validateCron(activation.Spec.Schedule.Start); err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "schedule", "start"),
				activation.Spec.Schedule.Start,
				fmt.Sprintf("invalid cron expression: %v", err),
			))
		}
		if _, err := validateCron(activation.Spec.Schedule.End); err != nil {
			errs = append(errs, field.Invalid(
				field.NewPath("spec", "schedule", "end"),
				activation.Spec.Schedule.End,
				fmt.Sprintf("invalid cron expression: %v", err),
			))
		}
		if activation.Spec.Schedule.Timezone != "" {
			if _, err := time.LoadLocation(activation.Spec.Schedule.Timezone); err != nil {
				errs = append(errs, field.Invalid(
					field.NewPath("spec", "schedule", "timezone"),
					activation.Spec.Schedule.Timezone,
					fmt.Sprintf("invalid timezone: %v", err),
				))
			}
		}
	}

	if len(errs) > 0 {
		return errs.ToAggregate()
	}
	return nil
}
