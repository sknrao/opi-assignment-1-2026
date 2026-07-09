// Command dpuf-adapter is the singleton host-cluster controller for pattern (e)
// (arch §2). It registers the mirrored OPI types and starts the two reconcilers.
//
// This wiring is deliberately minimal — enough to prove the reconcilers satisfy
// controller-runtime's manager API and that the scheme registration compiles.
// A real deployment adds leader election, health probes, metrics, and the DPF
// unstructured watch sources (all marked TODO(dpuf) in the controller).
package main

import (
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	adapter "github.com/opiproject/opi-nvidia-dpf-adapter"
	opiv1 "github.com/opiproject/opi-nvidia-dpf-adapter/api/opi/v1"
	"github.com/opiproject/opi-nvidia-dpf-adapter/internal/translate"
)

var scheme = runtime.NewScheme()

func init() {
	// Register the mirrored OPI CRD types. In a full build the DPF GVKs are
	// handled via the unstructured client, so they need no scheme entry.
	utilruntime.Must(opiv1.AddToScheme(scheme))
}

func main() {
	var dpfNamespace string
	flag.StringVar(&dpfNamespace, "dpf-namespace", "dpf-operator-system",
		"namespace where DPF objects are created")
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	tr := translate.NewDPFTranslator()

	// v1 wiring: a single fleet. Swapping SingleFleetResolver for a multi-fleet
	// resolver (keyed on FleetLabel, per-fleet credentials) is drop-in — the
	// ClusterResolver interface is identical (arch §9, Q4).
	fleet := adapter.Fleet{
		Name:         "default",
		Namespace:    dpfNamespace,
		NodeSelector: map[string]string{"dpu.nvidia.com/enabled": "true"},
		DPUFlavor:    "bf3-default",
		Probe:        adapter.NewStaticProbe(ctrl.Log.WithName("dpucluster"), true),
	}
	resolver := &adapter.SingleFleetResolver{Fleet: fleet}

	if err := (&adapter.ServiceFunctionChainReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Translator: tr,
		Fleets:     resolver,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ServiceFunctionChain")
		os.Exit(1)
	}

	if err := (&adapter.DataProcessingUnitReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Translator: tr,
		Fleets:     resolver,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DataProcessingUnit")
		os.Exit(1)
	}

	setupLog.Info("starting dpuf-adapter manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager exited with error")
		os.Exit(1)
	}
}
