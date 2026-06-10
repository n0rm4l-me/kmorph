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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

// ClusterProfileReconciler reconciles a ClusterProfile object.
// Its sole responsibility is to keep ClusterProfile status up to date
// (e.g. which activations reference it). Patch application is handled
// by ProfileActivationReconciler.
type ClusterProfileReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=config.kmorph.io,resources=clusterprofiles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=config.kmorph.io,resources=clusterprofiles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=config.kmorph.io,resources=clusterprofiles/finalizers,verbs=update

func (r *ClusterProfileReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	profile := &configv1alpha1.ClusterProfile{}
	if err := r.Get(ctx, req.NamespacedName, profile); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Find all ProfileActivations referencing this profile.
	activationList := &configv1alpha1.ProfileActivationList{}
	if err := r.List(ctx, activationList); err != nil {
		return ctrl.Result{}, err
	}

	var refs []string
	for _, a := range activationList.Items {
		if a.Spec.ProfileRef == profile.Name {
			refs = append(refs, a.Name)
		}
	}

	patch := client.MergeFrom(profile.DeepCopy())
	profile.Status.Activations = refs
	if err := r.Status().Patch(ctx, profile, patch); err != nil {
		log.Error(err, "failed to update ClusterProfile status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterProfileReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.ClusterProfile{}).
		Named("clusterprofile").
		Complete(r)
}
