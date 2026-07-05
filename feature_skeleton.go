// Package bridge contains the foundational skeleton for integrating NVIDIA's
// DOCA Platform Framework (DPF) with the OPI DPU Operator, per the design in
// architecture_design.md ("Federated Two-Phase Integration").
//
// This file is intentionally self-contained (stdlib only) so it compiles
// standalone without requiring the actual opi-project/dpu-operator or
// NVIDIA/doca-platform go modules to be vendored. In a real PR, the types
// marked "// MIRRORS: <real type>" below would be replaced by imports of the
// actual upstream packages:
//
//   opi "github.com/opiproject/dpu-operator/api/v1"
//   opiplatform "github.com/opiproject/dpu-operator/internal/platform"
//   opiplugin "github.com/opiproject/dpu-operator/internal/daemon/plugin"
//   dpfv1alpha1 "github.com/NVIDIA/doca-platform/api/provisioning/v1alpha1"
//
// Two integration surfaces are represented here, matching the two halves of
// the architecture:
//
//   1. Handoff:  DPUReadinessWatcher   (Phase 1 -> Phase 2 trigger)
//   2. Runtime:  NvidiaDetector + NvidiaVspPlugin (implements OPI's existing
//                VendorDetector / VendorPlugin extensibility interfaces)
package bridge

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// Section 0: Minimal stand-ins for upstream types
//
// These exist only so this file compiles standalone. Field/method shapes are
// chosen to mirror what is documented for the real OPI and DPF types.
// ============================================================================

// Condition mirrors k8s.io/apimachinery/pkg/apis/meta/v1.Condition (trimmed).
type Condition struct {
	Type    string
	Status  string // "True" | "False" | "Unknown"
	Reason  string
	Message string
}

// ObjectMeta mirrors metav1.ObjectMeta (trimmed to what we use here).
type ObjectMeta struct {
	Name        string
	Namespace   string
	Labels      map[string]string
	Annotations map[string]string
}

// ---- DPF-side stand-ins (MIRRORS: NVIDIA/doca-platform api/provisioning/v1alpha1) ----

// DPUPhase mirrors provisioning/v1alpha1.DPUPhase.
type DPUPhase string

const (
	DPUPhaseInitializing DPUPhase = "Initializing"
	DPUPhaseOSInstalling DPUPhase = "OS Installing"
	DPUPhaseReady        DPUPhase = "Ready"
	DPUPhaseError        DPUPhase = "Error"
)

// DPUStatus mirrors provisioning/v1alpha1.DPUStatus (trimmed).
type DPUStatus struct {
	Phase      DPUPhase
	NodeName   string
	Conditions []Condition
}

// DPUSpec mirrors provisioning/v1alpha1.DPUSpec (trimmed to fields the
// handoff cares about).
type DPUSpec struct {
	DPUFlavor string
}

// DPU mirrors NVIDIA DPF's DPU CRD. This design NEVER writes to this type;
// it is read-only from OPI's perspective.
type DPU struct {
	ObjectMeta
	Spec   DPUSpec
	Status DPUStatus
}

// ---- OPI-side stand-ins (MIRRORS: opiproject/dpu-operator api/v1) ----

// DataProcessingUnitSpec mirrors opi api/v1.DataProcessingUnitSpec.
type DataProcessingUnitSpec struct {
	DpuProductName string // e.g. "Intel IPU E2100", "Marvell DPU", "NVIDIA BlueField"
	IsDpuSide      bool
	NodeName       string
}

// DataProcessingUnitStatus mirrors opi api/v1.DataProcessingUnitStatus.
type DataProcessingUnitStatus struct {
	Conditions []Condition
}

// DataProcessingUnit mirrors opi api/v1.DataProcessingUnit. This is the ONLY
// object type the bridge is allowed to write.
type DataProcessingUnit struct {
	ObjectMeta
	Spec   DataProcessingUnitSpec
	Status DataProcessingUnitStatus
}

// ============================================================================
// Section 0.1: Minimal client abstractions
//
// Real code would use controller-runtime's client.Client / client.Reader.
// These narrow interfaces exist so DPUReadinessWatcher's dependencies (and
// its read/write boundary) are explicit and testable without a live cluster.
// ============================================================================

// DPUReader is a read-only view onto DPF's DPU objects. The bridge MUST NOT
// be given a writer for this type -- enforced here at the interface level,
// not just by convention.
type DPUReader interface {
	ListDPUs(ctx context.Context) ([]DPU, error)
}

// DataProcessingUnitWriter is the only write surface the bridge is granted.
type DataProcessingUnitWriter interface {
	// GetByName returns (obj, true, nil) if found, (nil, false, nil) if not
	// found, or (nil, false, err) on any other error.
	GetByName(ctx context.Context, namespace, name string) (*DataProcessingUnit, bool, error)
	CreateOrUpdate(ctx context.Context, obj *DataProcessingUnit) error
}

// ============================================================================
// Section 1: Handoff -- DPUReadinessWatcher
//
// Single responsibility: watch DPF's DPU objects; when one becomes Ready,
// create/update the corresponding OPI DataProcessingUnit. One-directional.
// Idempotent. Never reads back OPI status, never writes to DPF.
// ============================================================================

const (
	// NvidiaDpuProductName is the vendor string used to route reconciliation
	// to the NVIDIA VendorDetector (see Section 2).
	NvidiaDpuProductName = "NVIDIA BlueField"

	// dpuTraceAnnotation records which DPF DPU produced this OPI CR, purely
	// for operator debuggability -- it is never read back by any controller.
	dpuTraceAnnotation = "bridge.opi.dev/source-dpf-dpu"

	targetNamespace = "openshift-dpu"
)

// DPUReadinessWatcher implements the Phase-1-to-Phase-2 handoff described in
// architecture_design.md Section 5.1.
type DPUReadinessWatcher struct {
	dpuReader DPUReader
	opiWriter DataProcessingUnitWriter

	// pollInterval controls how often ListDPUs is polled in Run(). A real
	// implementation would instead use a controller-runtime watch/informer;
	// polling is used here to keep the skeleton dependency-free.
	pollInterval time.Duration

	mu       sync.Mutex
	observed map[string]DPUPhase // last observed phase per DPU name, for logging/no-op detection
}

// NewDPUReadinessWatcher constructs a watcher. Returns an error if either
// dependency is nil, since a watcher with a nil writer would silently do
// nothing forever -- fail loudly instead.
func NewDPUReadinessWatcher(reader DPUReader, writer DataProcessingUnitWriter, pollInterval time.Duration) (*DPUReadinessWatcher, error) {
	if reader == nil {
		return nil, errors.New("bridge: DPUReader must not be nil")
	}
	if writer == nil {
		return nil, errors.New("bridge: DataProcessingUnitWriter must not be nil")
	}
	if pollInterval <= 0 {
		pollInterval = 30 * time.Second
	}
	return &DPUReadinessWatcher{
		dpuReader:    reader,
		opiWriter:    writer,
		pollInterval: pollInterval,
		observed:     make(map[string]DPUPhase),
	}, nil
}

// Run blocks, polling DPF's DPU objects until ctx is cancelled. In a real
// deployment this would be replaced by a controller-runtime Reconcile() loop
// registered against DPF's DPU GVK; the polling loop here is a stand-in that
// preserves the same reconcile-on-change semantics.
func (w *DPUReadinessWatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.reconcileOnce(ctx); err != nil {
				log.Printf("bridge: DPUReadinessWatcher reconcile error: %v", err)
			}
		}
	}
}

// reconcileOnce performs a single pass: list DPF DPUs, and for each one that
// is Ready and identifiable as a BlueField device, ensure a corresponding
// OPI DataProcessingUnit exists.
func (w *DPUReadinessWatcher) reconcileOnce(ctx context.Context) error {
	dpus, err := w.dpuReader.ListDPUs(ctx)
	if err != nil {
		return fmt.Errorf("listing DPF DPUs: %w", err)
	}

	for _, dpu := range dpus {
		if err := w.reconcileSingle(ctx, dpu); err != nil {
			// Log and continue -- one bad DPU object should not block the
			// rest of the fleet from being handed off.
			log.Printf("bridge: failed to reconcile DPF DPU %q: %v", dpu.Name, err)
		}
	}
	return nil
}

func (w *DPUReadinessWatcher) reconcileSingle(ctx context.Context, dpu DPU) error {
	w.mu.Lock()
	lastPhase, seen := w.observed[dpu.Name]
	w.observed[dpu.Name] = dpu.Status.Phase
	w.mu.Unlock()

	if dpu.Status.Phase != DPUPhaseReady {
		return nil // Not ready yet; nothing to hand off.
	}
	if !w.isBlueField(dpu) {
		return nil // Ready, but not an NVIDIA BlueField device -- not our concern.
	}
	if seen && lastPhase == DPUPhaseReady {
		// Already observed as Ready previously; CreateOrUpdate below is
		// idempotent regardless, but skip the noisy log/round-trip.
		return w.ensureDataProcessingUnit(ctx, dpu)
	}

	log.Printf("bridge: DPF DPU %q reached Ready on node %q; creating OPI DataProcessingUnit", dpu.Name, dpu.Status.NodeName)
	return w.ensureDataProcessingUnit(ctx, dpu)
}

// isBlueField applies the narrow vendor check documented in
// architecture_design.md Section 5.1. A real implementation would likely
// check DPUFlavor content or a label rather than a substring match; this is
// left simple deliberately since flavor-naming conventions were not
// verifiable from the source repositories.
func (w *DPUReadinessWatcher) isBlueField(dpu DPU) bool {
	return strings.Contains(strings.ToLower(dpu.Spec.DPUFlavor), "bluefield")
}

// ensureDataProcessingUnit performs the one narrow field translation this
// entire design requires (see architecture_design.md Section 5.1). It
// intentionally does NOT attempt to reconstruct DPF-only fields (BFB,
// install interface, node effect) on the OPI side, since those are
// meaningless once provisioning has completed.
func (w *DPUReadinessWatcher) ensureDataProcessingUnit(ctx context.Context, dpu DPU) error {
	name := fmt.Sprintf("dpu-%s", dpu.Name)

	existing, found, err := w.opiWriter.GetByName(ctx, targetNamespace, name)
	if err != nil {
		return fmt.Errorf("checking for existing DataProcessingUnit %q: %w", name, err)
	}

	desired := &DataProcessingUnit{
		ObjectMeta: ObjectMeta{
			Name:      name,
			Namespace: targetNamespace,
			Annotations: map[string]string{
				dpuTraceAnnotation: dpu.Name,
			},
		},
		Spec: DataProcessingUnitSpec{
			DpuProductName: NvidiaDpuProductName,
			IsDpuSide:      false,
			NodeName:       dpu.Status.NodeName,
		},
	}

	if found && existing.Spec == desired.Spec {
		return nil // Already correct; avoid a needless write.
	}

	return w.opiWriter.CreateOrUpdate(ctx, desired)
}

// ============================================================================
// Section 2: Runtime -- OPI's existing extensibility interfaces
//
// MIRRORS: opiproject/dpu-operator internal/platform.VendorDetector
//          opiproject/dpu-operator internal/daemon/plugin.VendorPlugin
//
// These interface definitions are reproduced here (not imported) purely so
// this file is standalone-compilable. In the real PR, NvidiaDetector and
// NvidiaVspPlugin would implement the actual upstream interfaces directly,
// with NO changes to DataProcessingUnitReconciler's dispatch logic --
// registration is a one-line addition to NewDpuDetectorManager's detector
// list.
// ============================================================================

// DpuIdentifier mirrors plugin.DpuIdentifier.
type DpuIdentifier string

// PCIDevice is a minimal stand-in for ghw.PCIDevice, trimmed to the fields
// vendor detection actually inspects.
type PCIDevice struct {
	Vendor  string
	Product string
	Address string
}

// Platform mirrors internal/platform.Platform (trimmed).
type Platform interface {
	PciDevices() ([]*PCIDevice, error)
}

// VendorDetector mirrors internal/platform.VendorDetector exactly in shape.
type VendorDetector interface {
	Name() string
	IsDpuPlatform(platform Platform) (bool, error)
	IsDPU(platform Platform, pci PCIDevice, existing []DpuIdentifier) (bool, error)
	GetDpuIdentifier(platform Platform, pci *PCIDevice) (DpuIdentifier, error)
	GetVendorName() string
	DpuPlatformName() string
}

// BridgePortRequest / BridgePort / NetworkFunction types are minimal
// stand-ins for the real OPI-API (github.com/opiproject/opi-api) request and
// response types used by plugin.VendorPlugin.
type BridgePortRequest struct {
	Name string
}

type BridgePort struct {
	Name   string
	Status string
}

type DeviceListResponse struct {
	Devices []string
}

// VendorPlugin mirrors internal/daemon/plugin.VendorPlugin.
type VendorPlugin interface {
	Start(ctx context.Context) (ip string, port int32, err error)
	Close()
	CreateBridgePort(req *BridgePortRequest) (*BridgePort, error)
	DeleteBridgePort(req *BridgePortRequest) error
	CreateNetworkFunction(input, output string) error
	DeleteNetworkFunction(input, output string) error
	GetDevices() (*DeviceListResponse, error)
	SetNumVfs(vfCount int32) (int32, error)
}

// ============================================================================
// Section 2.1: NvidiaDetector
// ============================================================================

const (
	// nvidiaVendorID and nvidiaBlueFieldDeviceID are placeholders. The real
	// PCI vendor/device IDs for BlueField hardware must be sourced from
	// NVIDIA's own documentation before this is production code -- NOT
	// verifiable from either source repository reviewed for this design.
	nvidiaVendorID          = "15b3" // Mellanox/NVIDIA networking vendor ID (placeholder, verify before use)
	nvidiaBlueFieldDeviceID = "TODO-VERIFY-DEVICE-ID"
)

// NvidiaDetector implements VendorDetector for NVIDIA BlueField DPUs.
// Structurally mirrors IntelDetector / MarvellDetector.
type NvidiaDetector struct{}

func NewNvidiaDetector() *NvidiaDetector {
	return &NvidiaDetector{}
}

func (d *NvidiaDetector) Name() string {
	return NvidiaDpuProductName
}

func (d *NvidiaDetector) GetVendorName() string {
	return "nvidia"
}

func (d *NvidiaDetector) DpuPlatformName() string {
	// Used to locate bindata/vsp/{this}/ -- see architecture_design.md 5.4.
	return "nvidia-bluefield"
}

func (d *NvidiaDetector) IsDpuPlatform(platform Platform) (bool, error) {
	devices, err := platform.PciDevices()
	if err != nil {
		return false, fmt.Errorf("nvidia detector: listing PCI devices: %w", err)
	}
	for _, dev := range devices {
		if dev.Vendor == nvidiaVendorID {
			return true, nil
		}
	}
	return false, nil
}

func (d *NvidiaDetector) IsDPU(platform Platform, pci PCIDevice, existing []DpuIdentifier) (bool, error) {
	if pci.Vendor != nvidiaVendorID {
		return false, nil
	}
	if pci.Product != nvidiaBlueFieldDeviceID {
		return false, nil
	}
	id, err := d.GetDpuIdentifier(platform, &pci)
	if err != nil {
		return false, err
	}
	for _, existingID := range existing {
		if existingID == id {
			return false, nil // Already accounted for (multi-port dedup).
		}
	}
	return true, nil
}

func (d *NvidiaDetector) GetDpuIdentifier(_ Platform, pci *PCIDevice) (DpuIdentifier, error) {
	if pci == nil {
		return "", errors.New("nvidia detector: nil PCI device")
	}
	sanitized := strings.ReplaceAll(pci.Address, ":", "-")
	return DpuIdentifier(fmt.Sprintf("nvidia-bluefield-%s", sanitized)), nil
}

// VspPlugin constructs the NVIDIA runtime plugin. Signature mirrors the real
// VendorDetector.VspPlugin method; extra dependencies (image manager, k8s
// client, path manager) are omitted here since they are not needed for the
// skeleton's compilation and are upstream types.
func (d *NvidiaDetector) VspPlugin(dpuMode bool, dpuIdentifier DpuIdentifier, socketPath string) (VendorPlugin, error) {
	return NewNvidiaVspPlugin(dpuMode, dpuIdentifier, socketPath), nil
}

// ============================================================================
// Section 2.2: NvidiaVspPlugin
//
// IMPORTANT: this is the component flagged in architecture_design.md
// Section 6 as depending on an unverified precondition -- namely, that
// DOCA/BlueField's on-box software exposes a local API this plugin can
// translate OPI's VendorPlugin calls into. The methods below are stubbed
// with clear TODOs rather than fabricated DOCA API calls, since no such
// API was discoverable/verifiable from the reviewed repositories.
// ============================================================================

// NvidiaVspPlugin implements VendorPlugin by translating OPI's bridge-port /
// network-function / device calls into local DOCA service calls.
type NvidiaVspPlugin struct {
	dpuMode       bool
	dpuIdentifier DpuIdentifier
	socketPath    string

	mu          sync.RWMutex
	initialized bool

	// docaClient would be the real DOCA/BlueField local API client once
	// Section 6's precondition is resolved. Left untyped/nil here.
	docaClient interface{}
}

func NewNvidiaVspPlugin(dpuMode bool, dpuIdentifier DpuIdentifier, socketPath string) *NvidiaVspPlugin {
	return &NvidiaVspPlugin{
		dpuMode:       dpuMode,
		dpuIdentifier: dpuIdentifier,
		socketPath:    socketPath,
	}
}

// Start mirrors GrpcPlugin.Start(): establish the local connection and
// perform whatever initialization handshake is required, then report back
// an address other OPI components can reach this plugin at.
//
// TODO(blocking, see architecture_design.md Section 6): replace the stub
// below with a real connection to DOCA's local control-plane API once its
// existence and shape are confirmed. Until then this returns a
// not-implemented error rather than pretending to succeed.
func (p *NvidiaVspPlugin) Start(ctx context.Context) (string, int32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.socketPath == "" {
		return "", 0, errors.New("nvidia vsp plugin: socketPath must not be empty")
	}

	// Placeholder handshake. Real implementation: dial p.socketPath over a
	// Unix socket via gRPC (mirroring GrpcPlugin.ensureConnected), then send
	// an Init RPC carrying p.dpuMode and p.dpuIdentifier.
	if err := p.ensureConnected(ctx); err != nil {
		return "", 0, fmt.Errorf("nvidia vsp plugin: connecting: %w", err)
	}

	p.initialized = true
	return "127.0.0.1", 0, errNotImplemented("Start: DOCA local API handshake")
}

func (p *NvidiaVspPlugin) ensureConnected(_ context.Context) error {
	// TODO: dial p.socketPath, same pattern as GrpcPlugin.ensureConnected.
	return nil
}

func (p *NvidiaVspPlugin) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.initialized = false
	p.docaClient = nil
}

func (p *NvidiaVspPlugin) CreateBridgePort(req *BridgePortRequest) (*BridgePort, error) {
	if !p.isInitialized() {
		return nil, errors.New("nvidia vsp plugin: not initialized")
	}
	if req == nil {
		return nil, errors.New("nvidia vsp plugin: nil BridgePortRequest")
	}
	// TODO(blocking): translate into a DOCA Flow / OVS-DPU bridge-port
	// creation call. See architecture_design.md Section 6.
	return nil, errNotImplemented("CreateBridgePort")
}

func (p *NvidiaVspPlugin) DeleteBridgePort(req *BridgePortRequest) error {
	if !p.isInitialized() {
		return errors.New("nvidia vsp plugin: not initialized")
	}
	return errNotImplemented("DeleteBridgePort")
}

func (p *NvidiaVspPlugin) CreateNetworkFunction(input, output string) error {
	if !p.isInitialized() {
		return errors.New("nvidia vsp plugin: not initialized")
	}
	return errNotImplemented("CreateNetworkFunction")
}

func (p *NvidiaVspPlugin) DeleteNetworkFunction(input, output string) error {
	if !p.isInitialized() {
		return errors.New("nvidia vsp plugin: not initialized")
	}
	return errNotImplemented("DeleteNetworkFunction")
}

func (p *NvidiaVspPlugin) GetDevices() (*DeviceListResponse, error) {
	if !p.isInitialized() {
		return nil, errors.New("nvidia vsp plugin: not initialized")
	}
	return nil, errNotImplemented("GetDevices")
}

func (p *NvidiaVspPlugin) SetNumVfs(vfCount int32) (int32, error) {
	if !p.isInitialized() {
		return 0, errors.New("nvidia vsp plugin: not initialized")
	}
	if vfCount < 0 {
		return 0, fmt.Errorf("nvidia vsp plugin: invalid vfCount %d", vfCount)
	}
	return 0, errNotImplemented("SetNumVfs")
}

func (p *NvidiaVspPlugin) isInitialized() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.initialized
}

// errNotImplemented produces a consistent, greppable error so it's obvious
// in logs/tests which methods are still blocked on Section 6's spike.
func errNotImplemented(method string) error {
	return fmt.Errorf("nvidia vsp plugin: %s not implemented pending DOCA local-API verification (see architecture_design.md Section 6)", method)
}

// ============================================================================
// Section 3: Detector registration
//
// MIRRORS: internal/platform.NewDpuDetectorManager's existing detector list.
// This is the ONLY change needed to OPI's dispatch logic -- appending one
// element to a slice. Reproduced here as a standalone function so the
// skeleton demonstrates the registration shape without requiring the real
// DpuDetectorManager type.
// ============================================================================

// RegisterNvidiaDetector demonstrates the one-line, additive registration
// this design requires. In the real codebase this would be a single new
// entry in NewDpuDetectorManager's `detectors` slice literal, alongside
// NewIntelDetector() and NewMarvellDetector().
func RegisterNvidiaDetector(existing []VendorDetector) []VendorDetector {
	return append(existing, NewNvidiaDetector())
}

// ============================================================================
// Section 4: Wiring example (illustrative main, not executed in tests)
// ============================================================================

// fakeDPUReader and fakeOPIWriter below exist only to demonstrate how
// DPUReadinessWatcher is constructed and exercised; they are not part of
// the production surface.
type fakeDPUReader struct{ dpus []DPU }

func (f *fakeDPUReader) ListDPUs(_ context.Context) ([]DPU, error) {
	return f.dpus, nil
}

type fakeOPIWriter struct {
	mu    sync.Mutex
	store map[string]*DataProcessingUnit
}

func newFakeOPIWriter() *fakeOPIWriter {
	return &fakeOPIWriter{store: make(map[string]*DataProcessingUnit)}
}

func (f *fakeOPIWriter) GetByName(_ context.Context, namespace, name string) (*DataProcessingUnit, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.store[namespace+"/"+name]
	return obj, ok, nil
}

func (f *fakeOPIWriter) CreateOrUpdate(_ context.Context, obj *DataProcessingUnit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.store[obj.Namespace+"/"+obj.Name] = obj
	return nil
}

// Example wires the two halves of the design together end-to-end using the
// in-memory fakes above, purely to demonstrate the intended call shape. Not
// invoked automatically; kept as a documented entry point for manual/local
// experimentation (e.g. from a _test.go file or a throwaway main package).
func Example(ctx context.Context) error {
	reader := &fakeDPUReader{
		dpus: []DPU{
			{
				ObjectMeta: ObjectMeta{Name: "dpu-bf3-node1"},
				Spec:       DPUSpec{DPUFlavor: "bluefield3-default"},
				Status:     DPUStatus{Phase: DPUPhaseReady, NodeName: "node-1"},
			},
		},
	}
	writer := newFakeOPIWriter()

	watcher, err := NewDPUReadinessWatcher(reader, writer, time.Second)
	if err != nil {
		return err
	}

	if err := watcher.reconcileOnce(ctx); err != nil {
		return err
	}

	dpu, found, err := writer.GetByName(ctx, targetNamespace, "dpu-dpu-bf3-node1")
	if err != nil {
		return err
	}
	if !found {
		return errors.New("example: expected DataProcessingUnit to have been created")
	}

	log.Printf("bridge: example handoff produced DataProcessingUnit %+v", dpu.Spec)

	var detectors []VendorDetector
	detectors = RegisterNvidiaDetector(detectors)
	log.Printf("bridge: registered detectors: %v", detectorNames(detectors))

	return nil
}

func detectorNames(detectors []VendorDetector) []string {
	names := make([]string, 0, len(detectors))
	for _, d := range detectors {
		names = append(names, d.Name())
	}
	return names
}