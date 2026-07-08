// Package nvidiavsp sketches the NVIDIA BlueField-3 integration for the OPI
// DPU operator, following the DPF-backed VSP design: the VSP answers the
// operator's vendor plugin contract on the node, and fulfils it by authoring
// NVIDIA DPF custom resources instead of programming hardware itself.
//
// This file is a compilable skeleton, not a working implementation. Types
// marked "mirror:" are minimal local copies of contracts that live in the
// dpu-operator repo (internal/platform, internal/daemon/plugin, dpu-api) or
// in vendored dependencies (ghw, opi-api); in a real PR the detector would
// implement the upstream interfaces directly and this package would import
// them. Keeping the mirrors local lets the skeleton build standalone with
// only k8s.io/apimachinery as an external dependency.
package nvidiavsp

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ----------------------------------------------------------------------------
// Identity: how a BlueField-3 is recognised and named.
// ----------------------------------------------------------------------------

const (
	// PCI vendor ID shared by Mellanox/NVIDIA networking silicon.
	pciVendorNvidia = "15b3"

	// The label the VSP sets on a host node to opt it into the OPI-owned
	// DPUSet. This is the single provisioning signal crossing from OPI to
	// DPF: one bit, one writer (the VSP).
	provisioningLabel = "opi.dpu/managed"

	// Finalizer placed on OPI CRs whose deletion requires the VSP to tear
	// down DPF objects it authored. The VSP only ever waits on deletions it
	// issued itself, so out-of-band DPF removal cannot wedge an OPI CR.
	vspFinalizer = "dpu.opi.io/nvidia-vsp"
)

// blueFieldDeviceIDs lists the PCI device IDs the detector accepts. The
// values must be verified against the mlx5 driver tables before production
// use; a2dc is the BlueField-3 integrated controller.
var blueFieldDeviceIDs = map[string]string{
	"a2dc": "BlueField-3",
}

// DpuIdentifier mirror: internal/daemon/plugin.DpuIdentifier.
type DpuIdentifier string

// PCIDevice mirror: the subset of ghw.PCIDevice the detector consumes.
type PCIDevice struct {
	Address  string
	VendorID string
	DeviceID string
	Serial   string
	IsVF     bool
}

// Platform mirror: the subset of internal/platform.Platform the detector
// consumes. On the DPU side detection is by DMI product name, matching how
// the Intel and Marvell detectors decide they are running on their DPU.
type Platform interface {
	Product() (string, error)
	PciDevices() ([]PCIDevice, error)
}

// NvidiaBlueFieldDetector implements the VendorDetector contract from
// internal/platform/vendordetector.go. It is intentionally boring: the
// interesting behaviour lives behind the VSP socket, which is exactly how
// the operator wants vendors to arrive.
type NvidiaBlueFieldDetector struct{}

func NewNvidiaBlueFieldDetector() *NvidiaBlueFieldDetector {
	return &NvidiaBlueFieldDetector{}
}

func (d *NvidiaBlueFieldDetector) Name() string            { return "NVIDIA BlueField-3" }
func (d *NvidiaBlueFieldDetector) GetVendorName() string   { return "nvidia" }
func (d *NvidiaBlueFieldDetector) DpuPlatformName() string { return "nvidia-bf3" }

// IsDPU reports whether a host-side PCI device is a BlueField the VSP should
// manage. VFs are excluded the same way the Intel detector excludes them:
// the DPU is the PF; VFs are what we hand out to pods.
func (d *NvidiaBlueFieldDetector) IsDPU(platform Platform, pci PCIDevice, seen []DpuIdentifier) (bool, error) {
	if pci.IsVF || pci.VendorID != pciVendorNvidia {
		return false, nil
	}
	if _, ok := blueFieldDeviceIDs[pci.DeviceID]; !ok {
		return false, nil
	}
	// Multi-port boards expose one PCI function per port with a shared
	// serial; deduplicate so one card yields one DataProcessingUnit.
	id, err := d.GetDpuIdentifier(platform, &pci)
	if err != nil {
		return false, err
	}
	for _, s := range seen {
		if s == id {
			return false, nil
		}
	}
	return true, nil
}

// IsDpuPlatform reports whether we are running on the BlueField ARM cores
// themselves (phase 2, when the daemon+VSP arrive there as a DPUService).
func (d *NvidiaBlueFieldDetector) IsDpuPlatform(platform Platform) (bool, error) {
	product, err := platform.Product()
	if err != nil {
		return false, fmt.Errorf("reading DMI product name: %w", err)
	}
	return strings.Contains(product, "BlueField"), nil
}

// GetDpuIdentifier derives the stable identity shared with DPF. Both systems
// observing the same board must agree on this value, because status
// projection joins the OPI and DPF views by identifier, not by object name.
func (d *NvidiaBlueFieldDetector) GetDpuIdentifier(_ Platform, pci *PCIDevice) (DpuIdentifier, error) {
	if pci.Serial == "" {
		return "", fmt.Errorf("device %s exposes no serial; cannot form a stable identifier", pci.Address)
	}
	return DpuIdentifier("nvidia-bf3-" + strings.ToLower(pci.Serial)), nil
}

func (d *NvidiaBlueFieldDetector) DpuPlatformIdentifier(platform Platform) (DpuIdentifier, error) {
	product, err := platform.Product()
	if err != nil {
		return "", err
	}
	return DpuIdentifier("nvidia-bf3-" + strings.ToLower(strings.ReplaceAll(product, " ", "-"))), nil
}

// ----------------------------------------------------------------------------
// DPF object references: the only DPF surface the VSP touches.
// ----------------------------------------------------------------------------

// GroupVersionKind mirror: apimachinery's schema.GroupVersionKind, kept local
// so the skeleton's import list stays at metav1 only.
type GroupVersionKind struct {
	Group   string
	Version string
	Kind    string
}

// The complete set of DPF kinds this integration is allowed to author.
// Confining them to one table makes the version-coupling risk auditable:
// when DPF revs its v1alpha1 APIs, everything that can break is listed here.
var (
	gvkBFB                 = GroupVersionKind{Group: "provisioning.dpu.nvidia.com", Version: "v1alpha1", Kind: "BFB"}
	gvkDPUSet              = GroupVersionKind{Group: "provisioning.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPUSet"}
	gvkDPUFlavor           = GroupVersionKind{Group: "provisioning.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPUFlavor"}
	gvkDPU                 = GroupVersionKind{Group: "provisioning.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPU"}
	gvkDPUServiceInterface = GroupVersionKind{Group: "svc.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPUServiceInterface"}
	gvkDPUServiceChain     = GroupVersionKind{Group: "svc.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPUServiceChain"}
)

// Object is the minimal shape the translator needs from any DPF resource.
type Object struct {
	GVK        GroupVersionKind
	Namespace  string
	Name       string
	Spec       map[string]any
	Conditions []metav1.Condition
	Phase      string
}

// KubeClient is the narrow client the VSP is given. Ensure is create-or-update
// keyed on GVK/namespace/name; combined with deterministic names it makes
// every write idempotent, which is what turns crash-retry into a no-op.
// In the real implementation this is a controller-runtime client; a fake
// implementation drives the envtest suite without BlueField hardware.
type KubeClient interface {
	Ensure(ctx context.Context, obj Object) error
	Get(ctx context.Context, gvk GroupVersionKind, namespace, name string) (*Object, bool, error)
	Delete(ctx context.Context, gvk GroupVersionKind, namespace, name string) error
	LabelNode(ctx context.Context, node, key, value string) error
}

// NodeOps is the local half of the VSP: the few operations that are genuinely
// node state with no DPF counterpart (runtime VF activation, representor
// lookup). Kept behind an interface so tests can run without sysfs.
type NodeOps interface {
	SetRuntimeVfCount(pfAddress string, count int) (applied int, err error)
	VfCeiling(pfAddress string) (int, error)
	ListVfs(pfAddress string) ([]PCIDevice, error)
}

// ----------------------------------------------------------------------------
// Mirrors of the wire types the VSP serves (dpu-api and opi-api).
// ----------------------------------------------------------------------------

// BridgePortRequest mirror: opi-api evpn-gw CreateBridgePortRequest, reduced
// to the fields the host-side attach path consumes.
type BridgePortRequest struct {
	Name       string
	MacAddress string
	VlanID     int
	VfAddress  string
}

// Device mirror: dpu-api Device.
type Device struct {
	ID     string
	Health string
}

// ----------------------------------------------------------------------------
// The VSP itself.
// ----------------------------------------------------------------------------

// Config is authored from DpuOperatorConfig-level settings by the operator.
type Config struct {
	Namespace   string
	BFBImageURL string
	FlavorName  string
	DpuSetName  string
	// AttachPollBudget bounds the cold-network path in CreateBridgePort.
	// The pre-plumbed fast path never waits.
	AttachPollBudget time.Duration
}

// NvidiaVsp implements the VendorPlugin contract (mirrored here) by
// delegating fleet state to DPF and keeping only genuinely node-local
// operations imperative. It is stateless: every answer is derived from the
// cluster and the node at call time.
type NvidiaVsp struct {
	cfg       Config
	kube      KubeClient
	node      NodeOps
	dpuID     DpuIdentifier
	nodeName  string
	pfAddress string
	dpuMode   bool
}

func NewNvidiaVsp(cfg Config, kube KubeClient, node NodeOps, nodeName string) *NvidiaVsp {
	return &NvidiaVsp{cfg: cfg, kube: kube, node: node, nodeName: nodeName}
}

// Init implements LifeCycleService.Init. It establishes provisioning intent
// and returns immediately; readiness is reported through DataProcessingUnit
// conditions, never awaited here. Safe to call any number of times.
func (v *NvidiaVsp) Init(ctx context.Context, dpuMode bool, dpuIdentifier string) error {
	v.dpuMode = dpuMode
	v.dpuID = DpuIdentifier(dpuIdentifier)

	if dpuMode {
		// Phase 2: on the ARM side there is no provisioning to request;
		// the fact that we are running at all means DPF delivered us.
		return nil
	}

	if _, found, err := v.kube.Get(ctx, GroupVersionKind{Group: "operator.dpu.nvidia.com", Version: "v1alpha1", Kind: "DPFOperatorConfig"}, v.cfg.Namespace, "dpfoperatorconfig"); err != nil {
		return err
	} else if !found {
		return fmt.Errorf("DPF is not installed (no DPFOperatorConfig in %s); refusing to manage %s", v.cfg.Namespace, dpuIdentifier)
	}

	// Provisioning intent: BFB and flavor ensured from operator config, and
	// the single label that lets the OPI-owned DPUSet select this node.
	if err := v.kube.Ensure(ctx, Object{GVK: gvkBFB, Namespace: v.cfg.Namespace, Name: names.bfb(v.cfg), Spec: map[string]any{"url": v.cfg.BFBImageURL}}); err != nil {
		return err
	}
	if err := v.kube.Ensure(ctx, Object{GVK: gvkDPUFlavor, Namespace: v.cfg.Namespace, Name: v.cfg.FlavorName, Spec: map[string]any{
		// VF ceiling and OVS hw-offload are provisioning-time state, owned
		// by DPF via this flavor. TODO: render nvconfig/ovs sections.
	}}); err != nil {
		return err
	}
	return v.kube.LabelNode(ctx, v.nodeName, provisioningLabel, "true")
}

// GetDevices implements DeviceService.GetDevices: existence from the local
// PCI tree, health from the DPF DPU object - two sources answering two
// different questions, merged read-only.
func (v *NvidiaVsp) GetDevices(ctx context.Context) (map[string]Device, error) {
	vfs, err := v.node.ListVfs(v.pfAddress)
	if err != nil {
		return nil, err
	}
	dpu, found, err := v.kube.Get(ctx, gvkDPU, v.cfg.Namespace, names.dpu(v.dpuID))
	if err != nil {
		return nil, err
	}
	health := "unknown"
	if found {
		health = healthFromDpuPhase(dpu.Phase)
	}
	out := make(map[string]Device, len(vfs))
	for _, vf := range vfs {
		out[vf.Address] = Device{ID: vf.Address, Health: health}
	}
	return out, nil
}

// SetNumVfs implements DeviceService.SetNumVfs, split per the ownership
// rules: the firmware ceiling belongs to DPF (flavor, provisioning-time);
// the runtime count is node state the VSP owns directly.
func (v *NvidiaVsp) SetNumVfs(ctx context.Context, count int) (int, error) {
	ceiling, err := v.node.VfCeiling(v.pfAddress)
	if err != nil {
		return 0, err
	}
	want := count
	if want > ceiling {
		// Applied count is reported truthfully; the constraint surfaces as
		// a VfCountConstrained condition via the next projection pass, and
		// lifting it requires a flavor change plus re-provision.
		want = ceiling
	}
	return v.node.SetRuntimeVfCount(v.pfAddress, want)
}

// CreateBridgePort implements the opi-api BridgePortService for pod attach.
// The declarative work happened at network-creation time (pre-plumbed
// DPUServiceInterfaces); this call resolves and confirms, and only on a cold
// network does it create-and-poll within a bounded budget. The daemon's CNI
// retry loop is the safety net beyond that budget.
func (v *NvidiaVsp) CreateBridgePort(ctx context.Context, req *BridgePortRequest) error {
	name := names.serviceInterface(v.dpuID, req.VfAddress)
	ifc, found, err := v.kube.Get(ctx, gvkDPUServiceInterface, v.cfg.Namespace, name)
	if err != nil {
		return err
	}
	if !found {
		if err := v.kube.Ensure(ctx, Object{GVK: gvkDPUServiceInterface, Namespace: v.cfg.Namespace, Name: name, Spec: map[string]any{
			"interfaceType": "vf",
			"vf":            map[string]any{"pciAddress": req.VfAddress, "vlan": req.VlanID},
		}}); err != nil {
			return err
		}
		ifc, err = v.pollInterfaceReady(ctx, name)
		if err != nil {
			return err
		}
	}
	if !interfaceReady(ifc) {
		return fmt.Errorf("service interface %s exists but is not synced; attach will be retried", name)
	}
	// TODO: per-port runtime settings (MAC pinning) via NodeOps.
	return nil
}

func (v *NvidiaVsp) DeleteBridgePort(ctx context.Context, req *BridgePortRequest) error {
	// Deleting only what we authored, by the same deterministic name.
	return v.kube.Delete(ctx, gvkDPUServiceInterface, v.cfg.Namespace, names.serviceInterface(v.dpuID, req.VfAddress))
}

// CreateNetworkFunction implements NetworkFunctionService: an OPI service
// function insertion becomes a DPUServiceChain switch entry between two
// interfaces DPF already knows about.
func (v *NvidiaVsp) CreateNetworkFunction(ctx context.Context, input, output, bridgeID string) error {
	name := names.serviceChain(v.dpuID, bridgeID, input, output)
	return v.kube.Ensure(ctx, Object{GVK: gvkDPUServiceChain, Namespace: v.cfg.Namespace, Name: name, Spec: map[string]any{
		"switches": []map[string]any{{
			"ports": []map[string]any{
				{"serviceInterface": input},
				{"serviceInterface": output},
			},
		}},
	}})
}

func (v *NvidiaVsp) DeleteNetworkFunction(ctx context.Context, input, output, bridgeID string) error {
	return v.kube.Delete(ctx, gvkDPUServiceChain, v.cfg.Namespace, names.serviceChain(v.dpuID, bridgeID, input, output))
}

// SetDpuNetworkConfig implements DpuNetworkConfigService. On BlueField,
// hardware offload is flavor state and effectively always on; a runtime
// disable has no DPF counterpart and is refused honestly rather than faked.
func (v *NvidiaVsp) SetDpuNetworkConfig(_ context.Context, isAccelerated bool) error {
	if !isAccelerated {
		return fmt.Errorf("disabling acceleration at runtime is not supported on nvidia-bf3: offload is set at provisioning time via DPUFlavor")
	}
	return nil
}

func (v *NvidiaVsp) pollInterfaceReady(ctx context.Context, name string) (*Object, error) {
	deadline := time.Now().Add(v.cfg.AttachPollBudget)
	for {
		ifc, found, err := v.kube.Get(ctx, gvkDPUServiceInterface, v.cfg.Namespace, name)
		if err != nil {
			return nil, err
		}
		if found && interfaceReady(ifc) {
			return ifc, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("interface %s not synced within %s", name, v.cfg.AttachPollBudget)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// ----------------------------------------------------------------------------
// Status projection: a pure function of observed DPF state.
// ----------------------------------------------------------------------------

// ProjectDpuConditions maps the DPF view of one device onto the conditions
// of its DataProcessingUnit CR. It must stay a pure function - no latching,
// no memory of previous readiness - so that a re-list after any number of
// missed events produces identical output (level-triggered by construction).
func ProjectDpuConditions(dpu *Object, dpuClusterReachable bool, now metav1.Time) []metav1.Condition {
	ready := func(status metav1.ConditionStatus, reason, msg string) metav1.Condition {
		return metav1.Condition{Type: "Ready", Status: status, Reason: reason, Message: msg, LastTransitionTime: now}
	}
	switch {
	case dpu == nil:
		return []metav1.Condition{ready(metav1.ConditionFalse, "AwaitingProvisioning", "no DPF DPU object observed for this identifier")}
	case !dpuClusterReachable:
		return []metav1.Condition{ready(metav1.ConditionFalse, "DpuClusterUnreachable", "datapath unaffected; new attaches will fail until the DPU cluster returns")}
	case dpu.Phase == "Ready":
		return []metav1.Condition{ready(metav1.ConditionTrue, "Provisioned", "")}
	case dpu.Phase == "Error":
		return []metav1.Condition{ready(metav1.ConditionFalse, "ProvisioningFailed", firstConditionMessage(dpu.Conditions))}
	default:
		return []metav1.Condition{ready(metav1.ConditionFalse, "Provisioning", "DPF phase: "+dpu.Phase)}
	}
}

// ----------------------------------------------------------------------------
// Level-triggered reconcile entrypoint (the "reconciliation loop adapter").
// ----------------------------------------------------------------------------

// Result mirror: controller-runtime's reconcile.Result.
type Result struct {
	RequeueAfter time.Duration
}

// Reconcile converges the world toward the OPI intent for one DPU. It is the
// loop the VSP's embedded reconciler runs on every relevant watch event and
// on a timer; every step is an idempotent ensure, so partial failure plus
// retry always converges.
func (v *NvidiaVsp) Reconcile(ctx context.Context) (Result, error) {
	if v.dpuMode {
		return Result{}, nil // phase 2: DPU-side loop is chain-only
	}
	if err := v.Init(ctx, v.dpuMode, string(v.dpuID)); err != nil {
		return Result{RequeueAfter: 10 * time.Second}, err
	}
	dpu, found, err := v.kube.Get(ctx, gvkDPU, v.cfg.Namespace, names.dpu(v.dpuID))
	if err != nil {
		return Result{RequeueAfter: 10 * time.Second}, err
	}
	if !found {
		dpu = nil
	}
	conditions := ProjectDpuConditions(dpu, true, metav1.Now())
	_ = conditions // TODO: write onto the DataProcessingUnit CR via the OPI client.
	return Result{RequeueAfter: time.Minute}, nil
}

// ----------------------------------------------------------------------------
// Deterministic naming: what makes retries and teardown safe.
// ----------------------------------------------------------------------------

type nameScheme struct{}

// names produces every object name this VSP will ever author. Determinism is
// a correctness property here, not a style choice: crash-retry converges and
// deletion knows exactly what to look for because names are derived, never
// generated.
var names nameScheme

func (nameScheme) bfb(cfg Config) string { return "opi-" + cfg.DpuSetName + "-bfb" }
func (nameScheme) dpu(id DpuIdentifier) string {
	return strings.ToLower(string(id))
}
func (nameScheme) serviceInterface(id DpuIdentifier, vfAddress string) string {
	return fmt.Sprintf("opi-%s-vf-%s", strings.ToLower(string(id)), sanitize(vfAddress))
}
func (nameScheme) serviceChain(id DpuIdentifier, bridgeID, input, output string) string {
	return fmt.Sprintf("opi-%s-%s-%s-to-%s", strings.ToLower(string(id)), sanitize(bridgeID), sanitize(input), sanitize(output))
}

func sanitize(s string) string {
	return strings.ToLower(strings.NewReplacer(":", "-", ".", "-", "_", "-", "/", "-").Replace(s))
}

func interfaceReady(o *Object) bool {
	if o == nil {
		return false
	}
	for _, c := range o.Conditions {
		if c.Type == "Ready" && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

func healthFromDpuPhase(phase string) string {
	if phase == "Ready" {
		return "Healthy"
	}
	return "NotReady"
}

func firstConditionMessage(conds []metav1.Condition) string {
	for _, c := range conds {
		if c.Status == metav1.ConditionFalse && c.Message != "" {
			return c.Message // DPF's own words, passed through verbatim
		}
	}
	return "provisioning failed; see DPF DPU object for detail"
}

// ----------------------------------------------------------------------------
// Bridge operator: lifecycle management of the two upstream operators.
// Added after reviewer feedback asked what LCM from the "bridge operator"
// would look like. Scope is deliberately narrow: it manages operator
// deployments and versions, never DPF hardware objects or OPI CR status -
// the single-writer table gains one row and loses none.
// ----------------------------------------------------------------------------

// DpuPlatformSpec is the single intent object a platform admin writes.
type DpuPlatformSpec struct {
	DpuOperator ManagedComponent `json:"dpuOperator"`
	Dpf         ManagedComponent `json:"dpf"`
	// Manual requires an admin bump for every upgrade; Auto follows the
	// matrix's newest tested pair.
	UpgradePolicy string `json:"upgradePolicy,omitempty"`
}

type ManagedComponent struct {
	Version string `json:"version"`
	// Manage false = brownfield adoption: observe and validate only.
	Manage bool `json:"manage"`
}

// VersionMatrix is compiled into each bridge release by the conformance CI:
// only release-tag pairs that passed the suite are present. It is the
// enforcement artifact behind the admission webhook, not documentation.
type VersionMatrix struct {
	BridgeVersion string
	Tested        []VersionPair
}

type VersionPair struct {
	DpuOperator string
	Dpf         string
}

// Supported answers the admission check for a requested pair.
func (m *VersionMatrix) Supported(dpuOp, dpf string) bool {
	for _, p := range m.Tested {
		if p.DpuOperator == dpuOp && p.Dpf == dpf {
			return true
		}
	}
	return false
}

// Upgrade ordering: bridge first (it carries the new matrix), DPF second
// (the deep upgrade, gated on a quiet platform), DPU operator last (its
// restart is safe: stateless VSP, daemon retries Init, datapath unaffected).
const (
	stepValidatePair      = "ValidatePairAgainstMatrix"
	stepPauseProvisioning = "PauseProvisioningIntent"
	stepAwaitQuiesce      = "AwaitTerminalDpusNoFlashInFlight"
	stepUpgradeDpf        = "UpgradeDpfCrdsThenHelmRelease"
	stepGateDpfHealthy    = "GateDpfHealthyDpusReady"
	stepUpgradeDpuOp      = "ApplyDpuOperatorManifests"
	stepRecordAndResume   = "RecordLastKnownGoodResumeProvisioning"
)

// upgradeSequence is data, not a state machine: the reconciler walks it and
// executes the first step whose postcondition does not yet hold, so a crash
// mid-upgrade resumes from observed state rather than from memory.
var upgradeSequence = []string{
	stepValidatePair,
	stepPauseProvisioning,
	stepAwaitQuiesce,
	stepUpgradeDpf,
	stepGateDpfHealthy,
	stepUpgradeDpuOp,
	stepRecordAndResume,
}

// BridgeReconciler owns operator deployments and nothing else.
type BridgeReconciler struct {
	matrix VersionMatrix
	kube   KubeClient
}

// Reconcile converges the installed operators toward DpuPlatformSpec. Like
// every loop in this design it is level-triggered: each step re-checks its
// postcondition before acting, and rollback (helm history for DPF, manifest
// re-apply for the DPU operator, CRDs forward-only) is just another
// convergence toward the recorded last-known-good pair.
func (b *BridgeReconciler) Reconcile(ctx context.Context, spec DpuPlatformSpec) (Result, error) {
	if !b.matrix.Supported(spec.DpuOperator.Version, spec.Dpf.Version) {
		// Belt and braces: the admission webhook should have rejected this,
		// and out-of-band drift lands here. Report, pause, never remediate
		// a running platform by downgrading it automatically.
		return Result{}, fmt.Errorf("version pair (dpu-operator %s, dpf %s) is outside the tested matrix for bridge %s",
			spec.DpuOperator.Version, spec.Dpf.Version, b.matrix.BridgeVersion)
	}
	for _, step := range upgradeSequence {
		_ = step // TODO: execute step if its postcondition does not hold.
	}
	return Result{RequeueAfter: time.Minute}, nil
}
