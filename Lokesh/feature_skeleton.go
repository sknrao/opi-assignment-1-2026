// Package main - foundational skeleton for the OPI DPU Operator's NVIDIA DPF
// integration adapter, described in architecture_design.md.
//
// Module: github.com/Lokesh0224/opi-nvidia-adapter
//
// This file is a design skeleton, not a production controller: it demonstrates
// the plugin interface, the NVIDIA adapter, the reconcile/backoff shape, the
// ownership-cascade helper, and the degraded-feature fallback -- using only
// standard upstream Kubernetes dependencies. DPF's own CRDs (DPUSet,
// DPUService) are represented as unstructured.Unstructured objects rather
// than imported vendor Go types, so this resolves cleanly under `go mod tidy`
// without requiring NVIDIA's private DPF module.
//

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"    
)

// -----------------------------------------------------------------------
// 1. Vendor-neutral OPI types
// -----------------------------------------------------------------------

// OpiDpuProfileSpec is a minimal, local representation of the OPI CRD spec.
// In the real operator this would be codegen'd from an actual CRD schema;
// here it stands in as the vendor-neutral "intent" the adapter translates.
type OpiDpuProfileSpec struct {
	DpuFlavor           string `json:"dpuFlavor"`
	EnableLoadBalancing bool   `json:"enableLoadBalancing"`
	EnableEbpfLB        bool   `json:"enableEbpfLB"`
}

type OpiDpuProfileStatus struct {
	Phase      string             `json:"phase"` // Pending | Ready | Degraded
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// OpiDpuProfile is the top-level, vendor-neutral custom resource. It never
// carries NVIDIA-, Intel-, or Marvell-specific fields -- see architecture
// doc §2.1 (Enforced Spec Normalization).
type OpiDpuProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              OpiDpuProfileSpec   `json:"spec"`
	Status            OpiDpuProfileStatus `json:"status,omitempty"`
}

// OpiDpuProfileList is the list type required by controller-runtime's scheme
// registration. Every custom resource needs a corresponding List type.
type OpiDpuProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpiDpuProfile `json:"items"`
}

// DeepCopyObject satisfies runtime.Object for OpiDpuProfileList.
func (in *OpiDpuProfileList) DeepCopyObject() runtime.Object {
	out := *in
	out.Items = make([]OpiDpuProfile, len(in.Items))
	for i := range in.Items {
		out.Items[i] = *in.Items[i].DeepCopyObject().(*OpiDpuProfile)
	}
	return &out
}

// DeepCopyObject satisfies runtime.Object (and therefore client.Object).
// A full codegen'd CRD type would get this from controller-gen; this
// skeleton implements a shallow copy sufficient for compilation.
func (in *OpiDpuProfile) DeepCopyObject() runtime.Object {
	out := *in
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
	return &out
}

// -----------------------------------------------------------------------
// Scheme registration for OpiDpuProfile
// -----------------------------------------------------------------------

// SchemeGroupVersion is the GVR identity for the OpiDpuProfile CRD.
var SchemeGroupVersion = schema.GroupVersion{Group: "opi.io", Version: "v1alpha1"}

// SchemeBuilder registers the OpiDpuProfile types with a runtime.Scheme.
var SchemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(SchemeGroupVersion,
		&OpiDpuProfile{},
		&OpiDpuProfileList{},
	)
	metav1.AddToGroupVersion(s, SchemeGroupVersion)
	return nil
})

// AddToScheme adds the OpiDpuProfile types to the provided scheme.
var AddToScheme = SchemeBuilder.AddToScheme

// ProviderStatus is what a plugin reports back about the downstream vendor
// operator's health.
type ProviderStatus struct {
	Phase           string
	DegradedReason  string
	DegradedMessage string
}

const (
	PhasePending  = "Pending"
	PhaseReady    = "Ready"
	PhaseDegraded = "Degraded"
)

// -----------------------------------------------------------------------
// 2. The core interface: OPI core code depends on this and NOTHING else.
// -----------------------------------------------------------------------

// OpiProviderPlugin is the strict interface separating the OPI core from any
// vendor implementation (Open-Closed Principle - architecture doc §2).
type OpiProviderPlugin interface {
	// Name identifies the plugin, e.g. "nvidia-dpf".
	Name() string

	// Supports reports whether this plugin should handle a node, based on
	// Node Feature Discovery (NFD) labels.
	Supports(nodeLabels map[string]string) bool

	// Translate expands vendor-neutral intent into the vendor's own CRD
	// objects. Must be pure/deterministic for idempotent reconciliation.
	Translate(profile *OpiDpuProfile) ([]*unstructured.Unstructured, error)

	// Status inspects the downstream vendor operator/CRDs and reports health.
	Status(ctx context.Context, c client.Client, profile *OpiDpuProfile) (ProviderStatus, error)
}

// -----------------------------------------------------------------------
// 3. Dynamic plugin registry (NFD-label-keyed dispatch)
// -----------------------------------------------------------------------

// ProviderRegistry holds all registered vendor plugins and resolves the
// correct one per node, without the reconciler ever branching on vendor.
type ProviderRegistry struct {
	plugins []OpiProviderPlugin
}

func NewProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{}
}

// Register adds a plugin to the registry. Called at operator boot for each
// vendor driver (NvidiaDpfAdapter, IntelAdapter, MarvellAdapter, ...).
func (r *ProviderRegistry) Register(p OpiProviderPlugin) {
	r.plugins = append(r.plugins, p)
}

// Resolve returns the first plugin that supports the given node's labels.
func (r *ProviderRegistry) Resolve(nodeLabels map[string]string) (OpiProviderPlugin, error) {
	for _, p := range r.plugins {
		if p.Supports(nodeLabels) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no registered provider supports the given node labels")
}

// -----------------------------------------------------------------------
// 4. NVIDIA DPF adapter implementation
// -----------------------------------------------------------------------

// NVIDIA-related GroupVersionKinds. Represented generically (not imported
// from a private DPF module) so this file has zero vendor-private deps.
var (
	dpuSetGVK = schema.GroupVersionKind{
		Group:   "provisioning.dpu.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DPUSet",
	}
	dpuServiceGVK = schema.GroupVersionKind{
		Group:   "svc.dpu.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DPUService",
	}
)

// The REAL, verified node-selection gate used by the canonical 
// opiproject/dpu-operator repository (applied via kubectl label node <name> dpu=true).
const dpuGateLabel = "dpu"

// This constant represents this design's alternative proposal for in-binary dispatch.
// NVIDIA's vendor ID (10de) is a real constant, but the PCI class code (0300) 
// is flagged as unverified and should be validated against live telemetry.
const nvidiaVendorNFDLabel = "feature.node.kubernetes.io/pci-0300_10de.present"

// NvidiaDpfAdapter implements OpiProviderPlugin for the NVIDIA DOCA Platform
// Framework (DPF) operator, expanding one OpiDpuProfile into both a DPUSet
// (node-level provisioning) and a DPUService (workload deployment).
type NvidiaDpfAdapter struct{}

func NewNvidiaDpfAdapter() *NvidiaDpfAdapter {
	return &NvidiaDpfAdapter{}
}

func (a *NvidiaDpfAdapter) Name() string { return "nvidia-dpf" }

func (a *NvidiaDpfAdapter) Supports(nodeLabels map[string]string) bool {
	// Verifies both the canonical cluster gate and our targeted hardware vendor match
	dpuPresent := nodeLabels[dpuGateLabel] == "true"
	nvidiaVendorMatch := nodeLabels[nvidiaVendorNFDLabel] == "true"
	return dpuPresent && nvidiaVendorMatch
}

// Translate expands the neutral profile into DPUSet + DPUService objects.
// This function is pure and deterministic (architecture doc §3.2:
// Atomic State Commits / Idempotency) -- calling it twice with the same
// profile produces identical desired state, so a partial failure can safely
// be retried by the next reconcile without duplicating resources.
func (a *NvidiaDpfAdapter) Translate(profile *OpiDpuProfile) ([]*unstructured.Unstructured, error) {
	dpuSet := &unstructured.Unstructured{}
	dpuSet.SetGroupVersionKind(dpuSetGVK)
	dpuSet.SetName(profile.Name + "-dpuset")
	dpuSet.SetNamespace(profile.Namespace)
	if err := unstructured.SetNestedField(dpuSet.Object, profile.Spec.DpuFlavor, "spec", "dpuFlavor"); err != nil {
		return nil, fmt.Errorf("setting dpuFlavor: %w", err)
	}
	setOwnerReference(dpuSet, profile)

	dpuService := &unstructured.Unstructured{}
	dpuService.SetGroupVersionKind(dpuServiceGVK)
	dpuService.SetName(profile.Name + "-dpuservice")
	dpuService.SetNamespace(profile.Namespace)

	// --- Degraded Feature Matrix (architecture doc §5) ---
	// eBPF-based programmable load balancing is not yet exposed by NVIDIA
	// DPF. Rather than hard-failing, fall back to DPF's standard HBN
	// pipeline filters and surface the gap via a condition + event.
	degraded := false
	lbMode := "none"
	if profile.Spec.EnableLoadBalancing {
		if profile.Spec.EnableEbpfLB {
			lbMode = "hbn-filters" // fallback, not "ebpf"
			degraded = true
		} else {
			lbMode = "hbn-filters"
		}
	}
	if err := unstructured.SetNestedField(dpuService.Object, lbMode, "spec", "loadBalancing", "mode"); err != nil {
		return nil, fmt.Errorf("setting loadBalancing mode: %w", err)
	}
	setOwnerReference(dpuService, profile)

	if degraded {
		meta.SetStatusCondition(&profile.Status.Conditions, metav1.Condition{
			Type:    "FeatureDegraded",
			Status:  metav1.ConditionTrue,
			Reason:  "EbpfLoadBalancingUnsupported",
			Message: "eBPF load balancing unsupported on NVIDIA DPF; falling back to HBN pipeline filters",
		})
	}

	return []*unstructured.Unstructured{dpuSet, dpuService}, nil
}

// Status checks whether the downstream DPF CRDs are present and reports the
// health of the generated objects, per architecture doc §3.3 (Loose
// Coupling & Status Co-dependence).
func (a *NvidiaDpfAdapter) Status(ctx context.Context, c client.Client, profile *OpiDpuProfile) (ProviderStatus, error) {
	dpuSet := &unstructured.Unstructured{}
	dpuSet.SetGroupVersionKind(dpuSetGVK)
	err := c.Get(ctx, client.ObjectKey{Name: profile.Name + "-dpuset", Namespace: profile.Namespace}, dpuSet)
	if err != nil {
		// In a full implementation this branches on apierrors.IsNotFound
		// vs. a genuine discovery/connection failure. Simplified here.
		return ProviderStatus{
			Phase:           PhaseDegraded,
			DegradedReason:  "DependencyMissing",
			DegradedMessage: "NVIDIA DPF operator unresponsive",
		}, nil
	}
	return ProviderStatus{Phase: PhaseReady}, nil
}

// setOwnerReference wires the Defensive Ownership Cascade (architecture doc
// §6): when the OpiDpuProfile is deleted, Kubernetes garbage collection
// atomically removes every object owned this way.
func setOwnerReference(obj *unstructured.Unstructured, owner *OpiDpuProfile) {
	blockDeletion := true
	obj.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         "opi.io/v1alpha1",
			Kind:               "OpiDpuProfile",
			Name:               owner.Name,
			UID:                owner.UID,
			BlockOwnerDeletion: &blockDeletion,
		},
	})
}

// -----------------------------------------------------------------------
// 5. Reconciler: concurrency ceiling + exponential backoff
// -----------------------------------------------------------------------

// OpiDpuProfileReconciler is the single, vendor-blind reconcile loop. It
// never branches on vendor identity -- it resolves a plugin from the
// registry and delegates entirely.
type OpiDpuProfileReconciler struct {
	client.Client
	Registry *ProviderRegistry
}

// backoffSchedule implements the 5s -> 10s -> 30s -> 60s exponential
// backoff described in architecture doc §3.3, keyed by consecutive failure
// count on the object (tracked via an annotation in a full implementation).
func backoffSchedule(consecutiveFailures int) time.Duration {
	schedule := []time.Duration{5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	if consecutiveFailures >= len(schedule) {
		return schedule[len(schedule)-1]
	}
	if consecutiveFailures < 0 {
		consecutiveFailures = 0
	}
	return schedule[consecutiveFailures]
}

func (r *OpiDpuProfileReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	profile := &OpiDpuProfile{}
	if err := r.Get(ctx, req.NamespacedName, profile); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Defensive Metadata Guard: Ensures profile.Name is never zero-valued during local
	// mock execution or un-serialized scheme initialization pipelines.
	if profile.Name == "" {
		profile.Name = req.Name
	}
	if profile.Namespace == "" {
		profile.Namespace = req.Namespace
	}

	// In a full implementation, nodeLabels are fetched from the Node object
	// referenced by the profile's placement/selector. Simulated here to match
	// both the canonical cluster gate and our targeted hardware vendor proposal.
	nodeLabels := map[string]string{
		dpuGateLabel:         "true",
		nvidiaVendorNFDLabel: "true",
	}

	provider, err := r.Registry.Resolve(nodeLabels)
	if err != nil {
		return reconcile.Result{}, err
	}

	desired, err := provider.Translate(profile)
	if err != nil {
		return reconcile.Result{}, err
	}

	logger := logf.FromContext(ctx)

	for _, obj := range desired {
		// Server-side apply patch, never a full overwrite (architecture doc
		// §3.2: Three-Way Merge Patches).
		if err := r.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner("opi-operator")); err != nil {
			logger.Error(err, "failed to apply desired object", "object", obj.GetName())
			return reconcile.Result{RequeueAfter: backoffSchedule(0)}, nil
		}
	}

	status, err := provider.Status(ctx, r.Client, profile)
	if err != nil {
		logger.Error(err, "failed to check provider status")
		return reconcile.Result{RequeueAfter: backoffSchedule(1)}, nil
	}

	profile.Status.Phase = status.Phase
	if err := r.Status().Update(ctx, profile); err != nil {
		return reconcile.Result{}, err
	}

	if status.Phase == PhaseDegraded {
		return reconcile.Result{RequeueAfter: backoffSchedule(0)}, nil
	}
	return reconcile.Result{}, nil
}

// -----------------------------------------------------------------------
// 6. Manager wiring (skeleton main)
// -----------------------------------------------------------------------

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	scheme := runtime.NewScheme()

	// Register OpiDpuProfile types so the manager can watch and cache them.
	if err := AddToScheme(scheme); err != nil {
		log.Fatalf("unable to add OPI scheme: %v", err)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: ":8090"},
	})
	if err != nil {
		log.Fatalf("unable to start manager: %v", err)
	}

	registry := NewProviderRegistry()
	registry.Register(NewNvidiaDpfAdapter())

	reconciler := &OpiDpuProfileReconciler{
		Client:   mgr.GetClient(),
		Registry: registry,
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&OpiDpuProfile{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		WithOptions(controllerOptions()).
		Complete(reconciler)
	if err != nil {
		log.Fatalf("unable to create controller: %v", err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatalf("problem running manager: %v", err)
	}
}

func controllerOptions() controller.Options {
	return controller.Options{MaxConcurrentReconciles: 2}
}