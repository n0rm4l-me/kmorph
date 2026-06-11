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

// Package e2e contains end-to-end tests for kmorph.
// Tests run against a real cluster (kind in CI, or any cluster with KUBECONFIG set).
// Run with: go test ./test/e2e/ -v -timeout 5m
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
)

const (
	testNamespace = "kmorph-e2e"
	pollInterval  = 2 * time.Second
	pollTimeout   = 90 * time.Second
)

func newClient(t *testing.T) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = configv1alpha1.AddToScheme(scheme)

	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("failed to get kubeconfig: %v", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return c
}

func setupNamespace(t *testing.T, c client.Client) {
	t.Helper()
	ctx := context.Background()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
	if err := c.Create(ctx, ns); err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("failed to create namespace: %v", err)
	}
	t.Cleanup(func() {
		_ = c.Delete(ctx, ns)
	})
}

func createTestDeployment(t *testing.T, c client.Client, name string, replicas int32, cpu string) *appsv1.Deployment {
	t.Helper()
	ctx := context.Background()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{"app": name, "kmorph-test": "true"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "app",
						Image: "gcr.io/distroless/static:nonroot",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: mustParseQuantity(cpu),
							},
						},
					}},
				},
			},
		},
	}
	if err := c.Create(ctx, dep); err != nil {
		t.Fatalf("failed to create deployment: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, dep) })
	return dep
}

// waitForDeploymentReplicas polls until the deployment has the expected replica count.
func waitForDeploymentReplicas(t *testing.T, c client.Client, name string, expected int32) {
	t.Helper()
	ctx := context.Background()
	err := wait.PollUntilContextTimeout(ctx, pollInterval, pollTimeout, true, func(ctx context.Context) (bool, error) {
		dep := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, dep); err != nil {
			return false, nil
		}
		return *dep.Spec.Replicas == expected, nil
	})
	if err != nil {
		dep := &appsv1.Deployment{}
		_ = c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: name}, dep)
		t.Fatalf("deployment %q did not reach %d replicas within %s (current: %d)",
			name, expected, pollTimeout, *dep.Spec.Replicas)
	}
}

// waitForActivationPhase polls until the activation reaches the expected phase.
func waitForActivationPhase(t *testing.T, c client.Client, name string, phase configv1alpha1.ActivationPhase) {
	t.Helper()
	ctx := context.Background()
	err := wait.PollUntilContextTimeout(ctx, pollInterval, pollTimeout, true, func(ctx context.Context) (bool, error) {
		a := &configv1alpha1.ProfileActivation{}
		if err := c.Get(ctx, types.NamespacedName{Name: name}, a); err != nil {
			return false, nil
		}
		return a.Status.Phase == phase, nil
	})
	if err != nil {
		a := &configv1alpha1.ProfileActivation{}
		_ = c.Get(ctx, types.NamespacedName{Name: name}, a)
		t.Fatalf("activation %q did not reach phase %q within %s (current: %q)",
			name, phase, pollTimeout, a.Status.Phase)
	}
}

// --- Tests ---

// TestBasicActivation verifies that a ClusterProfile is applied when a ProfileActivation is created.
func TestBasicActivation(t *testing.T) {
	c := newClient(t)
	setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, "test-app", 2, "200m")

	profile := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-minimal"},
		Spec: configv1alpha1.ClusterProfileSpec{
			DriftPolicy: configv1alpha1.DriftPolicyStrict,
			Patches: []configv1alpha1.ResourcePatch{{
				Target: configv1alpha1.ResourceTarget{
					Namespace: testNamespace,
					Kind:      "Deployment",
					Name:      "test-app",
				},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}}),
			}},
		},
	}
	if err := c.Create(ctx, profile); err != nil {
		t.Fatalf("create ClusterProfile: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, profile) })

	activation := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-activation"},
		Spec: configv1alpha1.ProfileActivationSpec{
			ProfileRef: "e2e-minimal",
			Priority:   0,
		},
	}
	if err := c.Create(ctx, activation); err != nil {
		t.Fatalf("create ProfileActivation: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, activation) })

	waitForActivationPhase(t, c, "e2e-activation", configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, "test-app", 1)
	t.Log("✓ ClusterProfile applied: replicas=1")
}

// TestPriorityPreemption verifies that a higher-priority activation preempts a lower one.
func TestPriorityPreemption(t *testing.T) {
	c := newClient(t)
	setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, "test-preempt", 3, "200m")

	minimal := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-preempt-minimal"},
		Spec: configv1alpha1.ClusterProfileSpec{
			Patches: []configv1alpha1.ResourcePatch{{
				Target:    configv1alpha1.ResourceTarget{Namespace: testNamespace, Kind: "Deployment", Name: "test-preempt"},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}}),
			}},
		},
	}
	sleep := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-preempt-sleep"},
		Spec: configv1alpha1.ClusterProfileSpec{
			Patches: []configv1alpha1.ResourcePatch{{
				Target:    configv1alpha1.ResourceTarget{Namespace: testNamespace, Kind: "Deployment", Name: "test-preempt"},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(0)}}),
			}},
		},
	}
	for _, p := range []*configv1alpha1.ClusterProfile{minimal, sleep} {
		if err := c.Create(ctx, p); err != nil {
			t.Fatalf("create ClusterProfile %s: %v", p.Name, err)
		}
		t.Cleanup(func() { _ = c.Delete(ctx, p) })
	}

	// Low-priority fallback.
	fallback := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-fallback"},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: "e2e-preempt-minimal", Priority: 0},
	}
	if err := c.Create(ctx, fallback); err != nil {
		t.Fatalf("create fallback: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, fallback) })

	waitForActivationPhase(t, c, "e2e-fallback", configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, "test-preempt", 1)
	t.Log("✓ Fallback active: replicas=1")

	// High-priority sleep — should preempt.
	highPrio := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-high-prio"},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: "e2e-preempt-sleep", Priority: 100},
	}
	if err := c.Create(ctx, highPrio); err != nil {
		t.Fatalf("create high-prio: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, highPrio) })

	waitForActivationPhase(t, c, "e2e-high-prio", configv1alpha1.ActivationPhaseActive)
	waitForActivationPhase(t, c, "e2e-fallback", configv1alpha1.ActivationPhasePending)
	waitForDeploymentReplicas(t, c, "test-preempt", 0)
	t.Log("✓ High-priority preempted fallback: replicas=0")

	// Delete high-prio — fallback should resume.
	if err := c.Delete(ctx, highPrio); err != nil {
		t.Fatalf("delete high-prio: %v", err)
	}
	waitForActivationPhase(t, c, "e2e-fallback", configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, "test-preempt", 1)
	t.Log("✓ Fallback resumed after high-prio deleted: replicas=1")
}

// TestDurationExpiry verifies that a time-limited activation auto-expires and fails over.
func TestDurationExpiry(t *testing.T) {
	c := newClient(t)
	setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, "test-expiry", 1, "100m")

	minimal := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-expiry-minimal"},
		Spec: configv1alpha1.ClusterProfileSpec{
			Patches: []configv1alpha1.ResourcePatch{{
				Target:    configv1alpha1.ResourceTarget{Namespace: testNamespace, Kind: "Deployment", Name: "test-expiry"},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}}),
			}},
		},
	}
	boosted := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-expiry-boosted"},
		Spec: configv1alpha1.ClusterProfileSpec{
			Patches: []configv1alpha1.ResourcePatch{{
				Target:    configv1alpha1.ResourceTarget{Namespace: testNamespace, Kind: "Deployment", Name: "test-expiry"},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(3)}}),
			}},
		},
	}
	for _, p := range []*configv1alpha1.ClusterProfile{minimal, boosted} {
		if err := c.Create(ctx, p); err != nil {
			t.Fatalf("create profile %s: %v", p.Name, err)
		}
		t.Cleanup(func() { _ = c.Delete(ctx, p) })
	}

	fallback := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-expiry-fallback"},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: "e2e-expiry-minimal", Priority: 0},
	}
	if err := c.Create(ctx, fallback); err != nil {
		t.Fatalf("create fallback: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, fallback) })
	waitForActivationPhase(t, c, "e2e-expiry-fallback", configv1alpha1.ActivationPhaseActive)

	// Short-lived boost (10s).
	boost := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-expiry-boost"},
		Spec: configv1alpha1.ProfileActivationSpec{
			ProfileRef: "e2e-expiry-boosted",
			Priority:   50,
			Duration:   "10s",
		},
	}
	if err := c.Create(ctx, boost); err != nil {
		t.Fatalf("create boost: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, boost) })

	waitForActivationPhase(t, c, "e2e-expiry-boost", configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, "test-expiry", 3)
	t.Log("✓ Boosted active: replicas=3")

	waitForActivationPhase(t, c, "e2e-expiry-boost", configv1alpha1.ActivationPhaseExpired)
	waitForActivationPhase(t, c, "e2e-expiry-fallback", configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, "test-expiry", 1)
	t.Log("✓ Boost expired, fallback resumed: replicas=1")
}

// TestDriftEnforcement verifies that strict mode reverts manual changes.
func TestDriftEnforcement(t *testing.T) {
	c := newClient(t)
	setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, "test-drift", 2, "200m")

	profile := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-drift-strict"},
		Spec: configv1alpha1.ClusterProfileSpec{
			DriftPolicy: configv1alpha1.DriftPolicyStrict,
			Patches: []configv1alpha1.ResourcePatch{{
				Target:    configv1alpha1.ResourceTarget{Namespace: testNamespace, Kind: "Deployment", Name: "test-drift"},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}}),
			}},
		},
	}
	if err := c.Create(ctx, profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, profile) })

	activation := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-drift-activation"},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: "e2e-drift-strict", Priority: 0},
	}
	if err := c.Create(ctx, activation); err != nil {
		t.Fatalf("create activation: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(ctx, activation) })

	waitForActivationPhase(t, c, "e2e-drift-activation", configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, "test-drift", 1)

	// Manually change replicas — strict mode should revert.
	two := int32(3)
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "test-drift"}, dep); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	dep.Spec.Replicas = &two
	if err := c.Update(ctx, dep); err != nil {
		t.Fatalf("manual update: %v", err)
	}
	t.Log("manually set replicas=3, waiting for strict revert...")

	waitForDeploymentReplicas(t, c, "test-drift", 1)
	t.Log("✓ Strict mode reverted manual change: replicas=1")
}

// TestFinalizerCleanup verifies that deleting an activation removes the finalizer cleanly.
func TestFinalizerCleanup(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	profile := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-finalizer-profile"},
		Spec: configv1alpha1.ClusterProfileSpec{
			Patches: []configv1alpha1.ResourcePatch{{
				Target: configv1alpha1.ResourceTarget{
					Namespace: "default",
					Kind:      "ConfigMap",
					Name:      "kube-root-ca.crt",
				},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"metadata": map[string]interface{}{"labels": map[string]interface{}{"kmorph-test": "true"}}}),
			}},
		},
	}
	if err := c.Create(ctx, profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	defer func() { _ = c.Delete(ctx, profile) }()

	activation := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: "e2e-finalizer-activation"},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: "e2e-finalizer-profile", Priority: 0},
	}
	if err := c.Create(ctx, activation); err != nil {
		t.Fatalf("create activation: %v", err)
	}

	// Wait for finalizer to be added.
	err := wait.PollUntilContextTimeout(ctx, pollInterval, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		a := &configv1alpha1.ProfileActivation{}
		if err := c.Get(ctx, types.NamespacedName{Name: "e2e-finalizer-activation"}, a); err != nil {
			return false, nil
		}
		for _, f := range a.Finalizers {
			if f == "config.kmorph.io/cleanup" {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		t.Fatal("finalizer was never added to activation")
	}
	t.Log("✓ Finalizer added")

	// Delete — should succeed (finalizer removed by controller).
	if err := c.Delete(ctx, activation); err != nil {
		t.Fatalf("delete activation: %v", err)
	}

	err = wait.PollUntilContextTimeout(ctx, pollInterval, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		a := &configv1alpha1.ProfileActivation{}
		err := c.Get(ctx, types.NamespacedName{Name: "e2e-finalizer-activation"}, a)
		return errors.IsNotFound(err), nil
	})
	if err != nil {
		t.Fatal("activation was not fully deleted after finalizer cleanup")
	}
	t.Log("✓ Finalizer removed, activation fully deleted")
}

// --- helpers ---

func mustRawExtension(v interface{}) runtime.RawExtension {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustRawExtension: %v", err))
	}
	return runtime.RawExtension{Raw: raw}
}

func mustParseQuantity(s string) resource.Quantity {
	q, err := resource.ParseQuantity(s)
	if err != nil {
		panic(fmt.Sprintf("mustParseQuantity(%q): %v", s, err))
	}
	return q
}
