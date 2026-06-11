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

// Package metrics registers kmorph-specific Prometheus metrics.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ActiveActivations is the number of ProfileActivations currently in Active phase.
	ActiveActivations = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "kmorph",
		Name:      "active_activations_total",
		Help:      "Number of ProfileActivations currently in Active phase.",
	})

	// PendingActivations is the number of ProfileActivations in Pending phase.
	PendingActivations = prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: "kmorph",
		Name:      "pending_activations_total",
		Help:      "Number of ProfileActivations in Pending phase (in-window but preempted).",
	})

	// DriftEventsTotal counts drift detection events by profile and policy.
	DriftEventsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kmorph",
		Name:      "drift_events_total",
		Help:      "Total number of drift detection events.",
	}, []string{"profile", "drift_policy"})

	// PatchAppliedTotal counts successful patch applications.
	PatchAppliedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kmorph",
		Name:      "patch_applied_total",
		Help:      "Total number of patches successfully applied.",
	}, []string{"profile", "namespace", "kind", "patch_type"})

	// PatchFailedTotal counts failed patch applications.
	PatchFailedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kmorph",
		Name:      "patch_failed_total",
		Help:      "Total number of failed patch applications.",
	}, []string{"profile", "namespace", "kind"})

	// RolloutPromoteDuration tracks how long Rollout promotes take.
	RolloutPromoteDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "kmorph",
		Name:      "rollout_promote_duration_seconds",
		Help:      "Duration of Rollout promote operations in seconds.",
		Buckets:   []float64{5, 10, 30, 60, 120, 300, 600},
	}, []string{"namespace", "rollout", "result"})

	// ActivationSwitchTotal counts activation switches (priority winner changes).
	ActivationSwitchTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "kmorph",
		Name:      "activation_switch_total",
		Help:      "Total number of activation winner switches.",
	}, []string{"profile", "reason"})
)

func init() {
	metrics.Registry.MustRegister(
		ActiveActivations,
		PendingActivations,
		DriftEventsTotal,
		PatchAppliedTotal,
		PatchFailedTotal,
		RolloutPromoteDuration,
		ActivationSwitchTotal,
	)
}
