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

package main

import (
	"flag"
	"os"

	"fmt"
	"net/http"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	configv1alpha1 "github.com/n0rm4l-me/kmorph/api/v1alpha1"
	"github.com/n0rm4l-me/kmorph/internal/controller"
	_ "github.com/n0rm4l-me/kmorph/internal/metrics" // register custom Prometheus metrics
	"github.com/n0rm4l-me/kmorph/internal/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var probeAddr string
	var enableLeaderElection bool
	var enableWebhooks bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to. Use :8080 for HTTP or 0 to disable.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for controller manager.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", false, "Enable validating webhooks (requires TLS certificates).")

	opts := zap.Options{}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	logger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(logger)
	klog.SetLogger(logger)

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "c1ebd2f6.kmorph.io",
	})
	if err != nil {
		setupLog.Error(err, "failed to start manager")
		os.Exit(1)
	}

	if err := (&controller.ClusterProfileReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "failed to create controller", "controller", "ClusterProfile")
		os.Exit(1)
	}

	if err := (&controller.ProfileActivationReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("profileactivation-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "failed to create controller", "controller", "ProfileActivation")
		os.Exit(1)
	}

	if enableWebhooks {
		if err := (&webhook.ClusterProfileValidator{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "failed to create webhook", "webhook", "ClusterProfile")
			os.Exit(1)
		}
		if err := (&webhook.ProfileActivationValidator{Client: mgr.GetClient()}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "failed to create webhook", "webhook", "ProfileActivation")
			os.Exit(1)
		}
		setupLog.Info("webhooks enabled")
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "failed to set up health check")
		os.Exit(1)
	}
	// Readiness checks that the API server is reachable and the manager's cache is synced.
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "failed to set up ready check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("cache-sync", func(req *http.Request) error {
		if !mgr.GetCache().WaitForCacheSync(req.Context()) {
			return fmt.Errorf("cache not synced")
		}
		return nil
	}); err != nil {
		setupLog.Error(err, "failed to set up cache-sync check")
		os.Exit(1)
	}

	setupLog.Info("starting kmorph manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "failed to run manager")
		os.Exit(1)
	}
}
