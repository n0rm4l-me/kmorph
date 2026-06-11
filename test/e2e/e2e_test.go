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
// Run with: go test ./test/e2e/ -v -timeout 10m
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	pollInterval = 2 * time.Second
	pollTimeout  = 120 * time.Second
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

// testNamespace returns a unique namespace name for each test to prevent
// parallel test isolation issues. Each test gets its own namespace.
func testNamespace(t *testing.T) string {
	// Replace invalid chars and lowercase.
	name := strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))
	name = strings.ReplaceAll(name, "/", "-")
	// Trim to fit k8s namespace max length (63).
	if len(name) > 53 {
		name = name[:53]
	}
	return "kmorph-e2e-" + name[4:] // strip "test" prefix
}

func setupNamespace(t *testing.T, c client.Client) string {
	t.Helper()
	ctx := context.Background()
	ns := testNamespace(t)
	nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
	if err := c.Create(ctx, nsObj); err != nil && !errors.IsAlreadyExists(err) {
		t.Fatalf("failed to create namespace %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = c.Delete(context.Background(), nsObj)
	})
	return ns
}

func createTestDeployment(t *testing.T, c client.Client, ns, name string, replicas int32, cpu string) {
	t.Helper()
	ctx := context.Background()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
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
						Image: "registry.k8s.io/pause:3.9",
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU: resource.MustParse(cpu),
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
	t.Cleanup(func() { _ = c.Delete(context.Background(), dep) })
}

func waitForDeploymentReplicas(t *testing.T, c client.Client, ns, name string, expected int32) {
	t.Helper()
	ctx := context.Background()
	err := wait.PollUntilContextTimeout(ctx, pollInterval, pollTimeout, true, func(ctx context.Context) (bool, error) {
		dep := &appsv1.Deployment{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, dep); err != nil {
			return false, nil
		}
		return dep.Spec.Replicas != nil && *dep.Spec.Replicas == expected, nil
	})
	if err != nil {
		dep := &appsv1.Deployment{}
		_ = c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, dep)
		current := int32(-1)
		if dep.Spec.Replicas != nil {
			current = *dep.Spec.Replicas
		}
		t.Fatalf("deployment %q did not reach %d replicas within %s (current: %d)",
			name, expected, pollTimeout, current)
	}
}

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

// uniqueName returns a test-scoped unique name for cluster-scoped resources.
func uniqueName(t *testing.T, suffix string) string {
	base := strings.ToLower(strings.ReplaceAll(t.Name(), "_", "-"))
	base = strings.ReplaceAll(base, "/", "-")
	name := base + "-" + suffix
	if len(name) > 63 {
		name = name[len(name)-63:]
	}
	return name
}

// --- Tests ---

func TestBasicActivation(t *testing.T) {
	c := newClient(t)
	ns := setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, ns, "test-app", 2, "100m")

	profileName := uniqueName(t, "minimal")
	activationName := uniqueName(t, "activation")

	profile := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: profileName},
		Spec: configv1alpha1.ClusterProfileSpec{
			DriftPolicy: configv1alpha1.DriftPolicyStrict,
			Patches: []configv1alpha1.ResourcePatch{{
				Target:    configv1alpha1.ResourceTarget{Namespace: ns, Kind: "Deployment", Name: "test-app"},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}}),
			}},
		},
	}
	if err := c.Create(ctx, profile); err != nil {
		t.Fatalf("create ClusterProfile: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), profile) })

	activation := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: activationName},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: profileName, Priority: 0},
	}
	if err := c.Create(ctx, activation); err != nil {
		t.Fatalf("create ProfileActivation: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), activation) })

	waitForActivationPhase(t, c, activationName, configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, ns, "test-app", 1)
	t.Log("✓ Profile applied: replicas=1")
}

func TestPriorityPreemption(t *testing.T) {
	c := newClient(t)
	ns := setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, ns, "app", 3, "100m")

	minimalName := uniqueName(t, "minimal")
	sleepName := uniqueName(t, "sleep")
	fallbackName := uniqueName(t, "fallback")
	highName := uniqueName(t, "high")

	for name, replicas := range map[string]float64{minimalName: 1, sleepName: 0} {
		p := &configv1alpha1.ClusterProfile{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: configv1alpha1.ClusterProfileSpec{
				Patches: []configv1alpha1.ResourcePatch{{
					Target:    configv1alpha1.ResourceTarget{Namespace: ns, Kind: "Deployment", Name: "app"},
					PatchType: configv1alpha1.PatchTypeMerge,
					Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": replicas}}),
				}},
			},
		}
		if err := c.Create(ctx, p); err != nil {
			t.Fatalf("create profile %s: %v", name, err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), p) })
	}

	fallback := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: fallbackName},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: minimalName, Priority: 0},
	}
	if err := c.Create(ctx, fallback); err != nil {
		t.Fatalf("create fallback: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), fallback) })

	waitForActivationPhase(t, c, fallbackName, configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, ns, "app", 1)
	t.Log("✓ Fallback active: replicas=1")

	highPrio := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: highName},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: sleepName, Priority: 100},
	}
	if err := c.Create(ctx, highPrio); err != nil {
		t.Fatalf("create high-prio: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), highPrio) })

	waitForActivationPhase(t, c, highName, configv1alpha1.ActivationPhaseActive)
	waitForActivationPhase(t, c, fallbackName, configv1alpha1.ActivationPhasePending)
	waitForDeploymentReplicas(t, c, ns, "app", 0)
	t.Log("✓ High-priority preempted: replicas=0")

	if err := c.Delete(ctx, highPrio); err != nil {
		t.Fatalf("delete high-prio: %v", err)
	}
	waitForActivationPhase(t, c, fallbackName, configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, ns, "app", 1)
	t.Log("✓ Fallback resumed: replicas=1")
}

func TestDurationExpiry(t *testing.T) {
	c := newClient(t)
	ns := setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, ns, "app", 1, "100m")

	minimalName := uniqueName(t, "minimal")
	boostedName := uniqueName(t, "boosted")
	fallbackName := uniqueName(t, "fallback")
	boostName := uniqueName(t, "boost")

	for name, replicas := range map[string]float64{minimalName: 1, boostedName: 3} {
		p := &configv1alpha1.ClusterProfile{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec: configv1alpha1.ClusterProfileSpec{
				Patches: []configv1alpha1.ResourcePatch{{
					Target:    configv1alpha1.ResourceTarget{Namespace: ns, Kind: "Deployment", Name: "app"},
					PatchType: configv1alpha1.PatchTypeMerge,
					Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": replicas}}),
				}},
			},
		}
		if err := c.Create(ctx, p); err != nil {
			t.Fatalf("create profile %s: %v", name, err)
		}
		t.Cleanup(func() { _ = c.Delete(context.Background(), p) })
	}

	fallback := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: fallbackName},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: minimalName, Priority: 0},
	}
	if err := c.Create(ctx, fallback); err != nil {
		t.Fatalf("create fallback: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), fallback) })
	waitForActivationPhase(t, c, fallbackName, configv1alpha1.ActivationPhaseActive)

	boost := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: boostName},
		Spec: configv1alpha1.ProfileActivationSpec{
			ProfileRef: boostedName,
			Priority:   50,
			Duration:   "15s",
		},
	}
	if err := c.Create(ctx, boost); err != nil {
		t.Fatalf("create boost: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), boost) })

	waitForActivationPhase(t, c, boostName, configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, ns, "app", 3)
	t.Log("✓ Boost active: replicas=3")

	waitForActivationPhase(t, c, boostName, configv1alpha1.ActivationPhaseExpired)
	waitForActivationPhase(t, c, fallbackName, configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, ns, "app", 1)
	t.Log("✓ Boost expired, fallback resumed: replicas=1")
}

func TestDriftEnforcement(t *testing.T) {
	c := newClient(t)
	ns := setupNamespace(t, c)
	ctx := context.Background()

	createTestDeployment(t, c, ns, "app", 2, "100m")

	profileName := uniqueName(t, "strict")
	activationName := uniqueName(t, "activation")

	profile := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: profileName},
		Spec: configv1alpha1.ClusterProfileSpec{
			DriftPolicy: configv1alpha1.DriftPolicyStrict,
			Patches: []configv1alpha1.ResourcePatch{{
				Target:    configv1alpha1.ResourceTarget{Namespace: ns, Kind: "Deployment", Name: "app"},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"spec": map[string]interface{}{"replicas": float64(1)}}),
			}},
		},
	}
	if err := c.Create(ctx, profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), profile) })

	activation := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: activationName},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: profileName, Priority: 0},
	}
	if err := c.Create(ctx, activation); err != nil {
		t.Fatalf("create activation: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), activation) })

	waitForActivationPhase(t, c, activationName, configv1alpha1.ActivationPhaseActive)
	waitForDeploymentReplicas(t, c, ns, "app", 1)

	three := int32(3)
	dep := &appsv1.Deployment{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "app"}, dep); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	dep.Spec.Replicas = &three
	if err := c.Update(ctx, dep); err != nil {
		t.Fatalf("manual update: %v", err)
	}
	t.Log("manually set replicas=3, waiting for strict revert...")

	waitForDeploymentReplicas(t, c, ns, "app", 1)
	t.Log("✓ Strict mode reverted: replicas=1")
}

func TestFinalizerCleanup(t *testing.T) {
	c := newClient(t)
	ctx := context.Background()

	profileName := uniqueName(t, "profile")
	activationName := uniqueName(t, "activation")

	profile := &configv1alpha1.ClusterProfile{
		ObjectMeta: metav1.ObjectMeta{Name: profileName},
		Spec: configv1alpha1.ClusterProfileSpec{
			Patches: []configv1alpha1.ResourcePatch{{
				Target: configv1alpha1.ResourceTarget{
					Namespace: "default",
					Kind:      "ConfigMap",
					Name:      "kube-root-ca.crt",
				},
				PatchType: configv1alpha1.PatchTypeMerge,
				Patch:     mustRawExtension(map[string]interface{}{"metadata": map[string]interface{}{"labels": map[string]interface{}{"kmorph-e2e": "true"}}}),
			}},
		},
	}
	if err := c.Create(ctx, profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	t.Cleanup(func() { _ = c.Delete(context.Background(), profile) })

	activation := &configv1alpha1.ProfileActivation{
		ObjectMeta: metav1.ObjectMeta{Name: activationName},
		Spec:       configv1alpha1.ProfileActivationSpec{ProfileRef: profileName, Priority: 0},
	}
	if err := c.Create(ctx, activation); err != nil {
		t.Fatalf("create activation: %v", err)
	}

	// Wait for finalizer to be added.
	err := wait.PollUntilContextTimeout(ctx, pollInterval, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		a := &configv1alpha1.ProfileActivation{}
		if err := c.Get(ctx, types.NamespacedName{Name: activationName}, a); err != nil {
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
		t.Fatal("finalizer was never added")
	}
	t.Log("✓ Finalizer added")

	if err := c.Delete(ctx, activation); err != nil {
		t.Fatalf("delete activation: %v", err)
	}

	err = wait.PollUntilContextTimeout(ctx, pollInterval, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		a := &configv1alpha1.ProfileActivation{}
		err := c.Get(ctx, types.NamespacedName{Name: activationName}, a)
		return errors.IsNotFound(err), nil
	})
	if err != nil {
		t.Fatal("activation not deleted after finalizer cleanup")
	}
	t.Log("✓ Finalizer removed, activation deleted")
}

// --- helpers ---

func mustRawExtension(v interface{}) runtime.RawExtension {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mustRawExtension: %v", err))
	}
	return runtime.RawExtension{Raw: raw}
}
