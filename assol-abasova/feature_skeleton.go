// Package nvidiavsp is a foundational code skeleton for an NVIDIA
// Vendor Specific Plugin (VSP) for the OPI DPU operator, implemented as an
// adapter over the NVIDIA DOCA Platform Framework (DPF) operator.
//
// Design (see architecture_design.md):
//
//	dpu-daemon --OPI VSP gRPC--> VendorPlugin (this package)
//	                                  |
//	                                  v  Translator (OPI intent -> DPF CRs)
//	                             DPF operator --> BlueField-3 hardware
//	                                  |
//	                                  v  status watch
//	                             ReconcileAdapter --> OPI DPU CR conditions
//
// The skeleton intentionally uses only the standard library so that it
// compiles standalone (`go build`). In the real implementation:
//   - VendorPlugin methods are generated from the OPI VSP .proto contract.
//   - DPF types come from github.com/nvidia/doca-platform/api (v1alpha1).
//   - Client is satisfied by sigs.k8s.io/controller-runtime client.Client
//     using server-side apply with fieldManager "vsp-nvidia".
package nvidiavsp

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// OPI-facing contract (mirrors the VSP gRPC service consumed by dpu-daemon)
// ----------------------------------------------------------------------------

// Device is the vendor-neutral device identity returned to dpu-daemon and
// ultimately surfaced in the OPI `DPU` custom resource (`oc get dpu`).
type Device struct {
	ID       string // stable ID, e.g. "nvidia-bf3-0000-03-00.0"
	Product  string // e.g. "NVIDIA BlueField-3"
	PCIAddr  string
	NodeName string
	DPUSide  bool // true for the DPU-side representation
}

// NetworkFunction is one entry of ServiceFunctionChain.spec.networkFunctions.
type NetworkFunction struct {
	Name  string
	Image string
}

// ServiceFunctionChainIntent is the vendor-neutral intent handed to the VSP
// when the OPI operator reconciles a ServiceFunctionChain CR.
type ServiceFunctionChainIntent struct {
	Name             string
	Namespace        string
	NetworkFunctions []NetworkFunction
}

// VendorPlugin is the OPI VSP boundary. The OPI dpu-operator core knows
// nothing about NVIDIA; it only ever calls this contract.
type VendorPlugin interface {
	// Init prepares the vendor stack. For NVIDIA this ensures DPF is
	// installed and a DPFOperatorConfig exists (managed sub-operator).
	Init(ctx context.Context) error

	// GetDevices reports BlueField devices, backed by DPF discovery
	// (DPUNode / DPUDevice) rather than direct hardware probing.
	GetDevices(ctx context.Context) ([]Device, error)

	// CreateNetworkFunction realizes one SFC network function by
	// translating it to DPF resources (DPUDeployment et al.).
	CreateNetworkFunction(ctx context.Context, sfc ServiceFunctionChainIntent, nf NetworkFunction) error

	// DeleteNetworkFunction tears the translated DPF resources down
	// (cascade delete honored via owned-by labels / finalizers).
	DeleteNetworkFunction(ctx context.Context, sfc ServiceFunctionChainIntent, nf NetworkFunction) error
}

// ----------------------------------------------------------------------------
// Minimal typed stubs for the DPF (doca-platform) resources this adapter
// touches. Real code imports these from the DPF API module; the shapes here
// follow provisioning.dpu.nvidia.com/v1alpha1 and svc.dpu.nvidia.com/v1alpha1.
// ----------------------------------------------------------------------------

type ObjectMeta struct {
	Name      string
	Namespace string
	Labels    map[string]string
}

// Object is the minimal seam standing in for a Kubernetes runtime object.
type Object interface {
	GetKind() string
	GetMeta() *ObjectMeta
}

// DPUPhase mirrors DPF's DPU provisioning state machine.
type DPUPhase string

const (
	DPUPhaseInitializing DPUPhase = "Initializing"
	DPUPhaseOSInstalling DPUPhase = "OS Installing"
	DPUPhaseReady        DPUPhase = "Ready"
	DPUPhaseError        DPUPhase = "Error"
)

// DPFDPU is a stub of the DPF `DPU` provisioning resource.
type DPFDPU struct {
	Meta   ObjectMeta
	Status struct {
		Phase DPUPhase
	}
}

func (d *DPFDPU) GetKind() string      { return "DPU" }
func (d *DPFDPU) GetMeta() *ObjectMeta { return &d.Meta }

// DPUSet is a stub of DPF's DPUSet (fleet provisioning template: BFB image,
// flavor, node selector, rolling update strategy).
type DPUSet struct {
	Meta ObjectMeta
	Spec struct {
		DPUNodeSelector map[string]string
		BFBName         string
		DPUFlavor       string
	}
}

func (d *DPUSet) GetKind() string      { return "DPUSet" }
func (d *DPUSet) GetMeta() *ObjectMeta { return &d.Meta }

// HelmChartSource pins the Helm chart DPF's ArgoCD delivery deploys.
type HelmChartSource struct {
	Repo    string
	Chart   string
	Version string
}

// DPUDeployment is a stub of DPF's recommended high-level service resource;
// it fans out to DPUService / DPUServiceInterface / DPUServiceChain.
type DPUDeployment struct {
	Meta ObjectMeta
	Spec struct {
		HelmChart HelmChartSource
		// Values carries parameters for the generic "nf-wrapper" chart
		// that adapts arbitrary container images (OPI's SFC model) to
		// DPF's Helm-based delivery model.
		Values map[string]string
	}
}

func (d *DPUDeployment) GetKind() string      { return "DPUDeployment" }
func (d *DPUDeployment) GetMeta() *ObjectMeta { return &d.Meta }

// ----------------------------------------------------------------------------
// Client seam (stand-in for controller-runtime client with server-side apply)
// ----------------------------------------------------------------------------

type Client interface {
	Apply(ctx context.Context, obj Object) error         // SSA, fieldManager=vsp-nvidia
	Delete(ctx context.Context, obj Object) error
	ListDPUs(ctx context.Context) ([]*DPFDPU, error)
}

// ErrUnsupportedAPIVersion is returned when the detected DPF API version is
// not one the translation layer was built and conformance-tested against.
// The adapter fails closed rather than emitting resources it cannot verify.
var ErrUnsupportedAPIVersion = errors.New("nvidiavsp: unsupported DPF API version")

// ----------------------------------------------------------------------------
// Translation layer: pure OPI -> DPF mapping (unit-testable, golden files)
// ----------------------------------------------------------------------------

const (
	ownedByLabel   = "dpu.opiproject.org/owned-by"
	dpfNamespace   = "dpf-operator-system"
	wrapperChart   = "opi-nf-wrapper"
	wrapperRepo    = "oci://ghcr.io/opiproject/charts"
	wrapperVersion = "0.1.0"
)

// Translator maps vendor-neutral OPI intent onto DPF custom resources.
type Translator struct {
	// PinnedDPFVersion is the DPF API version this translator is
	// conformance-tested against, e.g. "v1alpha1".
	PinnedDPFVersion string
}

// ToDPUSet derives the fleet provisioning template that makes DPF adopt the
// nodes the OPI operator labeled for DPU management.
func (t *Translator) ToDPUSet(nodeSelector map[string]string, bfb, flavor string) *DPUSet {
	ds := &DPUSet{}
	ds.Meta = ObjectMeta{
		Name:      "opi-managed-bluefields",
		Namespace: dpfNamespace,
		Labels:    map[string]string{ownedByLabel: "dpu-operator-config"},
	}
	ds.Spec.DPUNodeSelector = nodeSelector
	ds.Spec.BFBName = bfb
	ds.Spec.DPUFlavor = flavor
	return ds
}

// ToDPUDeployment maps one ServiceFunctionChain network function to a
// DPUDeployment using the generic nf-wrapper Helm chart.
func (t *Translator) ToDPUDeployment(sfc ServiceFunctionChainIntent, nf NetworkFunction) (*DPUDeployment, error) {
	if nf.Image == "" {
		return nil, fmt.Errorf("network function %q in chain %s/%s has no image",
			nf.Name, sfc.Namespace, sfc.Name)
	}
	dd := &DPUDeployment{}
	dd.Meta = ObjectMeta{
		Name:      fmt.Sprintf("%s-%s", sfc.Name, nf.Name),
		Namespace: dpfNamespace,
		Labels: map[string]string{
			ownedByLabel: fmt.Sprintf("%s.%s", sfc.Namespace, sfc.Name),
		},
	}
	dd.Spec.HelmChart = HelmChartSource{Repo: wrapperRepo, Chart: wrapperChart, Version: wrapperVersion}
	dd.Spec.Values = map[string]string{
		"image":     nf.Image,
		"chainName": sfc.Name,
		"nfName":    nf.Name,
	}
	return dd, nil
}

// OPICondition is the vendor-neutral condition projected onto the OPI DPU CR.
type OPICondition struct {
	Type    string // "Ready"
	Status  string // "True" | "False" | "Unknown"
	Reason  string
	Message string
}

// MapDPUPhase converts DPF provisioning phases into OPI DPU CR conditions
// (status mirroring, Flow 3 in the architecture document).
func MapDPUPhase(p DPUPhase) OPICondition {
	switch p {
	case DPUPhaseReady:
		return OPICondition{Type: "Ready", Status: "True", Reason: "Provisioned"}
	case DPUPhaseError:
		return OPICondition{Type: "Ready", Status: "False", Reason: "DPUError"}
	case DPUPhaseInitializing, DPUPhaseOSInstalling:
		return OPICondition{Type: "Ready", Status: "False", Reason: "Provisioning"}
	default:
		return OPICondition{Type: "Ready", Status: "Unknown", Reason: "UnknownPhase",
			Message: fmt.Sprintf("unrecognized DPF phase %q", p)}
	}
}

// ----------------------------------------------------------------------------
// Reconcile adapter: level-triggered loop bridging OPI intent and DPF state
// ----------------------------------------------------------------------------

// StatusSink is where mirrored conditions are written (the OPI DPU CR /
// ServiceFunctionChain status in production).
type StatusSink interface {
	SetCondition(ctx context.Context, deviceID string, c OPICondition) error
}

// ReconcileAdapter is the core of the DPF-backed VSP. It implements
// VendorPlugin outward and drives DPF inward. All operations are idempotent:
// desired state is re-derived from intent on every pass (level-triggered).
type ReconcileAdapter struct {
	client     Client
	translator *Translator
	status     StatusSink

	mu     sync.Mutex
	// intents caches the last known OPI intent so drift in DPF resources
	// can be repaired even without a fresh gRPC call from dpu-daemon.
	intents map[string]ServiceFunctionChainIntent

	// ResyncPeriod bounds how stale mirrored status may become.
	ResyncPeriod time.Duration
}

// NewReconcileAdapter wires the adapter. supportedDPFVersion guards the
// translation contract (fail closed on unknown DPF releases).
func NewReconcileAdapter(c Client, s StatusSink, detectedDPFVersion string) (*ReconcileAdapter, error) {
	t := &Translator{PinnedDPFVersion: "v1alpha1"}
	if detectedDPFVersion != t.PinnedDPFVersion {
		return nil, fmt.Errorf("%w: detected %q, pinned %q",
			ErrUnsupportedAPIVersion, detectedDPFVersion, t.PinnedDPFVersion)
	}
	return &ReconcileAdapter{
		client:       c,
		translator:   t,
		status:       s,
		intents:      make(map[string]ServiceFunctionChainIntent),
		ResyncPeriod: 30 * time.Second,
	}, nil
}

// Init ensures the managed sub-operator preconditions. Production code Helm-
// installs DPF and applies a DPFOperatorConfig derived from DpuOperatorConfig.
func (r *ReconcileAdapter) Init(ctx context.Context) error {
	ds := r.translator.ToDPUSet(map[string]string{"dpu": "true"},
		"bf-bundle-default", "dpf-provisioning-default")
	return r.client.Apply(ctx, ds)
}

// GetDevices answers OPI discovery from DPF's view of the fleet.
func (r *ReconcileAdapter) GetDevices(ctx context.Context) ([]Device, error) {
	dpus, err := r.client.ListDPUs(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing DPF DPUs: %w", err)
	}
	out := make([]Device, 0, len(dpus))
	for _, d := range dpus {
		out = append(out, Device{
			ID:      d.Meta.Name,
			Product: "NVIDIA BlueField-3",
			DPUSide: false,
		})
	}
	return out, nil
}

// CreateNetworkFunction translates and applies; safe to call repeatedly.
func (r *ReconcileAdapter) CreateNetworkFunction(ctx context.Context, sfc ServiceFunctionChainIntent, nf NetworkFunction) error {
	dd, err := r.translator.ToDPUDeployment(sfc, nf)
	if err != nil {
		return err
	}
	if err := r.client.Apply(ctx, dd); err != nil {
		return fmt.Errorf("applying DPUDeployment %s: %w", dd.Meta.Name, err)
	}
	r.mu.Lock()
	r.intents[dd.Meta.Name] = sfc
	r.mu.Unlock()
	return nil
}

// DeleteNetworkFunction cascades teardown of the translated resources.
func (r *ReconcileAdapter) DeleteNetworkFunction(ctx context.Context, sfc ServiceFunctionChainIntent, nf NetworkFunction) error {
	dd, err := r.translator.ToDPUDeployment(sfc, nf)
	if err != nil {
		return err
	}
	if err := r.client.Delete(ctx, dd); err != nil {
		return fmt.Errorf("deleting DPUDeployment %s: %w", dd.Meta.Name, err)
	}
	r.mu.Lock()
	delete(r.intents, dd.Meta.Name)
	r.mu.Unlock()
	return nil
}

// MirrorStatusOnce performs one status-mirroring pass (Flow 3). In production
// this is watch-driven via controller-runtime; the periodic form here bounds
// staleness and doubles as drift repair.
func (r *ReconcileAdapter) MirrorStatusOnce(ctx context.Context) error {
	dpus, err := r.client.ListDPUs(ctx)
	if err != nil {
		return err
	}
	var firstErr error
	for _, d := range dpus {
		cond := MapDPUPhase(d.Status.Phase)
		if err := r.status.SetCondition(ctx, d.Meta.Name, cond); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Run drives periodic resync until the context is cancelled.
func (r *ReconcileAdapter) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.ResyncPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := r.MirrorStatusOnce(ctx); err != nil {
				// Log-and-continue in production; reconcile loops
				// must tolerate transient failures.
				_ = err
			}
		}
	}
}

// Compile-time guarantee that the adapter satisfies the OPI VSP contract.
var _ VendorPlugin = (*ReconcileAdapter)(nil)
