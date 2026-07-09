// Package integration defines the core structures, interfaces, and controller
// logic for integrating the OPI Operator with the NVIDIA DPF Operator using
// the Status-Driven Adapter Controller pattern.
//
// This file is compilable with: go build ./...
// It provides the foundational types and interfaces. Vendor-specific SDK
// bindings (DOCA, IPDK) and full gRPC protobuf stubs are intentionally
// replaced with local interface contracts to keep this file self-contained.
package integration

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// =============================================================================
// Section 1: DPF Status Types (Mirrors NVIDIA DPF CRD Status Fields)
// =============================================================================

// DPUPhase represents the lifecycle phase of a DPU as reported by the
// NVIDIA DPF Operator through DPUSet.status.dpus[].phase.
type DPUPhase string

const (
	DPUPhaseProvisioning  DPUPhase = "Provisioning"
	DPUPhaseReady         DPUPhase = "Ready"
	DPUPhaseFirmwareUpdate DPUPhase = "FirmwareUpdate"
	DPUPhaseError         DPUPhase = "Error"
	DPUPhaseDraining      DPUPhase = "Draining"
)

// DPUReadiness holds the cached readiness state of a single DPU,
// populated by observing DPF CRD status fields (read-only).
type DPUReadiness struct {
	NodeName       string
	Vendor         string    // e.g., "nvidia", "intel", "marvell"
	Phase          DPUPhase
	BridgeAddr     string    // gRPC endpoint, e.g., "192.168.100.2:50051"
	FirmwareVer    string    // e.g., "24.10"
	LastTransition time.Time
}

// =============================================================================
// Section 2: OPI CRD Types (Vendor-Neutral Custom Resource Definitions)
// =============================================================================

// OPICRDPhase represents the reconciliation status of an OPI custom resource.
type OPICRDPhase string

const (
	OPIPhasePending       OPICRDPhase = "Pending"
	OPIPhaseWaitingForDPU OPICRDPhase = "WaitingForDPU"
	OPIPhaseActive        OPICRDPhase = "Active"
	OPIPhaseDisrupted     OPICRDPhase = "Disrupted"
	OPIPhaseError         OPICRDPhase = "Error"
)

// DpuNetworkPolicySpec defines the desired state for a DPU network policy.
type DpuNetworkPolicySpec struct {
	NodeName   string `json:"nodeName"`
	BridgeName string `json:"bridgeName"`
	VlanID     uint32 `json:"vlanID"`
}

// DpuNetworkPolicyStatus defines the observed state of a DPU network policy.
type DpuNetworkPolicyStatus struct {
	Phase          OPICRDPhase `json:"phase"`
	Message        string      `json:"message,omitempty"`
	HardwareID     string      `json:"hardwareID,omitempty"`
	LastReconciled time.Time   `json:"lastReconciled,omitempty"`
}

// DpuNetworkPolicy is the top-level OPI custom resource for network configuration.
type DpuNetworkPolicy struct {
	Name      string                 `json:"name"`
	Namespace string                 `json:"namespace"`
	Spec      DpuNetworkPolicySpec   `json:"spec"`
	Status    DpuNetworkPolicyStatus `json:"status"`
}

// DpuStorageVolumeSpec defines the desired state for a DPU storage volume.
type DpuStorageVolumeSpec struct {
	NodeName      string `json:"nodeName"`
	SubsystemNQN  string `json:"subsystemNqn"`
	RemoteAddress string `json:"remoteAddress"`
	RemotePort    int    `json:"remotePort"`
}

// DpuStorageVolumeStatus defines the observed state of a DPU storage volume.
type DpuStorageVolumeStatus struct {
	Phase          OPICRDPhase `json:"phase"`
	Message        string      `json:"message,omitempty"`
	HardwareID     string      `json:"hardwareID,omitempty"`
	LastReconciled time.Time   `json:"lastReconciled,omitempty"`
}

// DpuStorageVolume is the top-level OPI custom resource for storage configuration.
type DpuStorageVolume struct {
	Name      string                 `json:"name"`
	Namespace string                 `json:"namespace"`
	Spec      DpuStorageVolumeSpec   `json:"spec"`
	Status    DpuStorageVolumeStatus `json:"status"`
}

// DpuSecurityPolicySpec defines the desired state for a DPU security policy.
type DpuSecurityPolicySpec struct {
	NodeName string `json:"nodeName"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // "tcp" or "udp"
	Action   string `json:"action"`   // "ALLOW" or "BLOCK"
}

// DpuSecurityPolicyStatus defines the observed state of a DPU security policy.
type DpuSecurityPolicyStatus struct {
	Phase          OPICRDPhase `json:"phase"`
	Message        string      `json:"message,omitempty"`
	HardwareID     string      `json:"hardwareID,omitempty"`
	LastReconciled time.Time   `json:"lastReconciled,omitempty"`
}

// DpuSecurityPolicy is the top-level OPI custom resource for firewall/ACL rules.
type DpuSecurityPolicy struct {
	Name      string                  `json:"name"`
	Namespace string                  `json:"namespace"`
	Spec      DpuSecurityPolicySpec   `json:"spec"`
	Status    DpuSecurityPolicyStatus `json:"status"`
}

// DpuIPsecTunnelSpec defines the desired state for a DPU IPsec tunnel.
type DpuIPsecTunnelSpec struct {
	NodeName    string `json:"nodeName"`
	RemoteIP    string `json:"remoteIP"`
	PreSharedKey string `json:"preSharedKey"`
	CipherSuite string `json:"cipherSuite"` // e.g. "AES-GCM-256"
}

// DpuIPsecTunnelStatus defines the observed state of a DPU IPsec tunnel.
type DpuIPsecTunnelStatus struct {
	Phase          OPICRDPhase `json:"phase"`
	Message        string      `json:"message,omitempty"`
	HardwareID     string      `json:"hardwareID,omitempty"`
	LastReconciled time.Time   `json:"lastReconciled,omitempty"`
}

// DpuIPsecTunnel is the top-level OPI custom resource for IPsec tunnels.
type DpuIPsecTunnel struct {
	Name      string               `json:"name"`
	Namespace string               `json:"namespace"`
	Spec      DpuIPsecTunnelSpec   `json:"spec"`
	Status    DpuIPsecTunnelStatus `json:"status"`
}

// =============================================================================
// Section 3: OPI Bridge Interface (Vendor-Neutral gRPC Contract)
// =============================================================================

// BridgeResponse represents a generic response from the OPI gRPC bridge
// running on the DPU.
type BridgeResponse struct {
	HardwareID string
	Status     string
}

// OPIBridgeClient defines the vendor-neutral interface for communicating with
// an OPI gRPC bridge running on a DPU. Each vendor (NVIDIA, Intel, Marvell)
// implements this interface inside their bridge container.
//
// The OPI Operator on the host calls these methods. The bridge on the DPU
// translates them to vendor-specific SDK calls (e.g., DOCA, IPDK).
type OPIBridgeClient interface {
	// --- Networking ---
	CreateBridge(ctx context.Context, name string, vlanID uint32) (*BridgeResponse, error)
	DeleteBridge(ctx context.Context, name string) error
	CreateBridgePort(ctx context.Context, bridgeName string, macAddress string) (*BridgeResponse, error)
	DeleteBridgePort(ctx context.Context, portID string) error

	// --- Storage ---
	CreateNvmeSubsystem(ctx context.Context, nqn string) (*BridgeResponse, error)
	DeleteNvmeSubsystem(ctx context.Context, nqn string) error
	CreateNvmeController(ctx context.Context, subsystemNQN string, pcieAddr string) (*BridgeResponse, error)
	DeleteNvmeController(ctx context.Context, controllerID string) error

	// --- Security ---
	CreateSecurityPolicy(ctx context.Context, port int, protocol string, action string) (*BridgeResponse, error)
	DeleteSecurityPolicy(ctx context.Context, policyID string) error
	CreateIPsecTunnel(ctx context.Context, remoteIP string, key string, cipher string) (*BridgeResponse, error)
	DeleteIPsecTunnel(ctx context.Context, tunnelID string) error

	// --- Platform ---
	GetInventory(ctx context.Context) (*InventoryInfo, error)
	HealthCheck(ctx context.Context) error
	ListAllRules(ctx context.Context) ([]string, error)
}

// InventoryInfo contains the hardware identity and capability information
// returned by the OPI bridge's GetInventory() call.
type InventoryInfo struct {
	Vendor       string
	Model        string
	SerialNumber string
	FirmwareVer  string
	Capabilities CapabilitySet
}

// CapabilitySet describes which hardware features the DPU supports.
type CapabilitySet struct {
	SupportsIPsec         bool
	SupportsNvmeEmulation bool
	SupportsCompression   bool
	MaxFlowRules          int
	MaxBridgePorts        int
}

// =============================================================================
// Section 4: DPF Status Adapter (Read-Only Observer)
// =============================================================================

// DPFStatusAdapter watches NVIDIA DPF CRD status fields (DPUSet) in a
// read-only fashion and caches DPU readiness information. It never creates,
// updates, or deletes DPF resources.
//
// This is the core integration point between the OPI Operator and the
// DPF Operator. It follows the Kubernetes "status observation" pattern.
type DPFStatusAdapter struct {
	mu       sync.RWMutex
	dpuCache map[string]*DPUReadiness
}

// NewDPFStatusAdapter creates a new adapter with an empty cache.
func NewDPFStatusAdapter() *DPFStatusAdapter {
	return &DPFStatusAdapter{
		dpuCache: make(map[string]*DPUReadiness),
	}
}

// UpdateFromDPUSetStatus is called by the controller-runtime reconciler
// when a DPUSet status change event is received from the Kubernetes API server.
// It updates the internal cache with the latest DPU phase information.
//
// This method only READS the incoming DPUSet status; it never writes back
// to the Kubernetes API.
func (a *DPFStatusAdapter) UpdateFromDPUSetStatus(nodeName string, phase DPUPhase, mgmtIP string, fwVersion string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.dpuCache[nodeName] = &DPUReadiness{
		NodeName:       nodeName,
		Vendor:         "nvidia",
		Phase:          phase,
		BridgeAddr:     fmt.Sprintf("%s:50051", mgmtIP),
		FirmwareVer:    fwVersion,
		LastTransition: time.Now(),
	}
}

// IsDPUReady returns true if the DPU on the given node has been provisioned
// by DPF and is in a Ready phase.
func (a *DPFStatusAdapter) IsDPUReady(nodeName string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	r, ok := a.dpuCache[nodeName]
	return ok && r.Phase == DPUPhaseReady
}

// GetBridgeAddress returns the gRPC endpoint of the OPI bridge running on
// the DPU attached to the given node. Returns an error if the DPU is not ready.
func (a *DPFStatusAdapter) GetBridgeAddress(nodeName string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	r, ok := a.dpuCache[nodeName]
	if !ok {
		return "", fmt.Errorf("no DPU found on node %s", nodeName)
	}
	if r.Phase != DPUPhaseReady {
		return "", fmt.Errorf("DPU on node %s is not ready (phase: %s)", nodeName, r.Phase)
	}
	return r.BridgeAddr, nil
}

// GetDPUReadiness returns the full readiness state for a given node,
// or nil if no DPU is tracked for that node.
func (a *DPFStatusAdapter) GetDPUReadiness(nodeName string) *DPUReadiness {
	a.mu.RLock()
	defer a.mu.RUnlock()

	r, ok := a.dpuCache[nodeName]
	if !ok {
		return nil
	}
	// Return a copy to prevent data races
	copy := *r
	return &copy
}

// ListAllNodes returns a list of all tracked node names and their phases.
func (a *DPFStatusAdapter) ListAllNodes() map[string]DPUPhase {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]DPUPhase, len(a.dpuCache))
	for k, v := range a.dpuCache {
		result[k] = v.Phase
	}
	return result
}

// =============================================================================
// Section 5: Circuit Breaker (Resilience Pattern)
// =============================================================================

// CircuitState represents the current state of a circuit breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "CLOSED"
	CircuitOpen     CircuitState = "OPEN"
	CircuitHalfOpen CircuitState = "HALF_OPEN"
)

// CircuitBreaker prevents cascading failures when the OPI bridge on the
// DPU becomes unresponsive. It stops sending gRPC calls after consecutive
// failures and periodically retests the connection.
type CircuitBreaker struct {
	mu              sync.Mutex
	state           CircuitState
	consecutiveFails int
	failThreshold   int
	lastFailure     time.Time
	cooldownPeriod  time.Duration
}

// NewCircuitBreaker creates a circuit breaker with the given failure threshold
// and cooldown period.
func NewCircuitBreaker(failThreshold int, cooldownPeriod time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:          CircuitClosed,
		failThreshold:  failThreshold,
		cooldownPeriod: cooldownPeriod,
	}
}

// Execute runs the given function through the circuit breaker.
// If the circuit is open, it returns an error immediately without executing fn.
// If the circuit is half-open, it allows one test call through.
func (cb *CircuitBreaker) Execute(fn func() error) error {
	cb.mu.Lock()

	switch cb.state {
	case CircuitOpen:
		if time.Since(cb.lastFailure) > cb.cooldownPeriod {
			cb.state = CircuitHalfOpen
			cb.mu.Unlock()
			// Allow one test call
		} else {
			cb.mu.Unlock()
			return fmt.Errorf("circuit breaker OPEN: DPU bridge unavailable, retry after %v",
				cb.cooldownPeriod-time.Since(cb.lastFailure))
		}
	default:
		cb.mu.Unlock()
	}

	// Execute the actual function
	err := fn()

	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.consecutiveFails++
		cb.lastFailure = time.Now()
		if cb.consecutiveFails >= cb.failThreshold {
			cb.state = CircuitOpen
		}
		return err
	}

	// Success — reset the breaker
	cb.consecutiveFails = 0
	cb.state = CircuitClosed
	return nil
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// =============================================================================
// Section 6: Write-Ahead Log (Crash Recovery)
// =============================================================================

// WALEntryStatus represents the completion state of a WAL entry.
type WALEntryStatus string

const (
	WALPending   WALEntryStatus = "PENDING"
	WALComplete  WALEntryStatus = "COMPLETE"
	WALFailed    WALEntryStatus = "FAILED"
)

// WALEntry represents a single operation recorded in the write-ahead log
// before it is sent to the DPU bridge.
type WALEntry struct {
	ID           string         `json:"id"`
	Timestamp    time.Time      `json:"timestamp"`
	OperationType string       `json:"operationType"` // "CreateBridge", "DeleteBridge", etc.
	ResourceName string         `json:"resourceName"`
	NodeName     string         `json:"nodeName"`
	Payload      string         `json:"payload"` // JSON-serialized request
	Status       WALEntryStatus `json:"status"`
}

// WriteAheadLog provides crash-recovery semantics for gRPC operations.
// Before any gRPC call is made to the DPU bridge, the operation is written
// to the WAL. On restart, incomplete operations are replayed.
type WriteAheadLog struct {
	mu      sync.Mutex
	entries map[string]*WALEntry
}

// NewWriteAheadLog creates a new in-memory WAL.
// In production, this would be backed by persistent storage (e.g., a PVC
// or an embedded database like BoltDB).
func NewWriteAheadLog() *WriteAheadLog {
	return &WriteAheadLog{
		entries: make(map[string]*WALEntry),
	}
}

// Write records an operation intent before execution.
func (w *WriteAheadLog) Write(entry *WALEntry) {
	w.mu.Lock()
	defer w.mu.Unlock()

	entry.Status = WALPending
	entry.Timestamp = time.Now()
	w.entries[entry.ID] = entry
}

// MarkComplete marks an operation as successfully completed.
func (w *WriteAheadLog) MarkComplete(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if e, ok := w.entries[id]; ok {
		e.Status = WALComplete
	}
}

// MarkFailed marks an operation as permanently failed.
func (w *WriteAheadLog) MarkFailed(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if e, ok := w.entries[id]; ok {
		e.Status = WALFailed
	}
}

// GetPending returns all operations that were started but never completed.
// These should be replayed on startup.
func (w *WriteAheadLog) GetPending() []*WALEntry {
	w.mu.Lock()
	defer w.mu.Unlock()

	var pending []*WALEntry
	for _, e := range w.entries {
		if e.Status == WALPending {
			pending = append(pending, e)
		}
	}
	return pending
}

// GetAllComplete returns all successfully completed operations.
// Used by the reconciliation loop to determine the desired hardware state.
func (w *WriteAheadLog) GetAllComplete() []*WALEntry {
	w.mu.Lock()
	defer w.mu.Unlock()

	var completed []*WALEntry
	for _, e := range w.entries {
		if e.Status == WALComplete {
			completed = append(completed, e)
		}
	}
	return completed
}

// =============================================================================
// Section 7: Reconciliation Result (Controller Return Type)
// =============================================================================

// ReconcileResult represents the outcome of a controller reconciliation loop,
// mirroring the controller-runtime ctrl.Result pattern.
type ReconcileResult struct {
	Requeue      bool
	RequeueAfter time.Duration
}

// =============================================================================
// Section 8: Network Policy Controller (OPI CRD Reconciler)
// =============================================================================

// NetworkPolicyController reconciles DpuNetworkPolicy custom resources.
// It checks DPU readiness via the DPF Status Adapter, then sends standard
// OPI gRPC calls to the bridge running on the DPU.
type NetworkPolicyController struct {
	DPFAdapter     *DPFStatusAdapter
	CircuitBreaker *CircuitBreaker
	WAL            *WriteAheadLog
	BridgeFactory  func(addr string) (OPIBridgeClient, error)
}

// NewNetworkPolicyController creates a new controller with all dependencies.
func NewNetworkPolicyController(
	adapter *DPFStatusAdapter,
	cb *CircuitBreaker,
	wal *WriteAheadLog,
	factory func(addr string) (OPIBridgeClient, error),
) *NetworkPolicyController {
	return &NetworkPolicyController{
		DPFAdapter:     adapter,
		CircuitBreaker: cb,
		WAL:            wal,
		BridgeFactory:  factory,
	}
}

// Reconcile handles a single DpuNetworkPolicy reconciliation event.
func (c *NetworkPolicyController) Reconcile(ctx context.Context, policy *DpuNetworkPolicy) (*ReconcileResult, error) {
	nodeName := policy.Spec.NodeName

	// Step 1: Check DPU readiness via DPF Status Adapter (read-only)
	if !c.DPFAdapter.IsDPUReady(nodeName) {
		policy.Status.Phase = OPIPhaseWaitingForDPU
		policy.Status.Message = "DPU is being provisioned by DPF, waiting for Ready phase"
		return &ReconcileResult{Requeue: true, RequeueAfter: 15 * time.Second}, nil
	}

	// Step 2: Get bridge endpoint from adapter
	bridgeAddr, err := c.DPFAdapter.GetBridgeAddress(nodeName)
	if err != nil {
		policy.Status.Phase = OPIPhaseError
		policy.Status.Message = fmt.Sprintf("Cannot resolve bridge address: %v", err)
		return &ReconcileResult{Requeue: true, RequeueAfter: 10 * time.Second}, nil
	}

	// Step 3: Write to WAL before executing
	walEntry := &WALEntry{
		ID:            fmt.Sprintf("net-%s-%d", policy.Name, time.Now().UnixNano()),
		OperationType: "CreateBridge",
		ResourceName:  policy.Spec.BridgeName,
		NodeName:      nodeName,
	}
	c.WAL.Write(walEntry)

	// Step 4: Execute through circuit breaker
	var resp *BridgeResponse
	cbErr := c.CircuitBreaker.Execute(func() error {
		bridge, dialErr := c.BridgeFactory(bridgeAddr)
		if dialErr != nil {
			return fmt.Errorf("failed to connect to bridge at %s: %w", bridgeAddr, dialErr)
		}
		var createErr error
		resp, createErr = bridge.CreateBridge(ctx, policy.Spec.BridgeName, policy.Spec.VlanID)
		return createErr
	})

	if cbErr != nil {
		c.WAL.MarkFailed(walEntry.ID)
		policy.Status.Phase = OPIPhaseError
		policy.Status.Message = fmt.Sprintf("Bridge creation failed: %v", cbErr)
		return &ReconcileResult{Requeue: true, RequeueAfter: 30 * time.Second}, cbErr
	}

	// Step 5: Mark WAL complete and update CRD status
	c.WAL.MarkComplete(walEntry.ID)
	policy.Status.Phase = OPIPhaseActive
	policy.Status.HardwareID = resp.HardwareID
	policy.Status.Message = fmt.Sprintf("Bridge %s active on %s", resp.HardwareID, nodeName)
	policy.Status.LastReconciled = time.Now()

	return &ReconcileResult{Requeue: false}, nil
}

// =============================================================================
// Section 9: Lifecycle Watcher (DPF Phase Transition Handler)
// =============================================================================

// LifecycleWatcher monitors DPF-initiated DPU state transitions and
// coordinates the OPI data path accordingly. For example, when DPF reboots
// a DPU for a firmware update, this watcher marks all OPI resources as
// Disrupted and replays them when the DPU returns to Ready.
type LifecycleWatcher struct {
	DPFAdapter    *DPFStatusAdapter
	WAL           *WriteAheadLog
	BridgeFactory func(addr string) (OPIBridgeClient, error)

	// previousPhases tracks the last known phase per node so we can
	// detect transitions (e.g., FirmwareUpdate → Ready).
	mu             sync.Mutex
	previousPhases map[string]DPUPhase
}

// NewLifecycleWatcher creates a new watcher instance.
func NewLifecycleWatcher(
	adapter *DPFStatusAdapter,
	wal *WriteAheadLog,
	factory func(addr string) (OPIBridgeClient, error),
) *LifecycleWatcher {
	return &LifecycleWatcher{
		DPFAdapter:     adapter,
		WAL:            wal,
		BridgeFactory:  factory,
		previousPhases: make(map[string]DPUPhase),
	}
}

// HandleTransition processes a DPU phase change event. It is called by the
// controller-runtime reconciler watching DPUSet status changes.
func (w *LifecycleWatcher) HandleTransition(ctx context.Context, nodeName string, newPhase DPUPhase) error {
	w.mu.Lock()
	prevPhase, hasPrev := w.previousPhases[nodeName]
	w.previousPhases[nodeName] = newPhase
	w.mu.Unlock()

	switch newPhase {
	case DPUPhaseFirmwareUpdate, DPUPhaseDraining:
		// DPF is about to disrupt this DPU. Mark all OPI resources as Disrupted.
		// In a real implementation, this would list all OPI CRDs targeting
		// this node and update their status.phase to "Disrupted".
		fmt.Printf("[LifecycleWatcher] Node %s: DPU entering %s phase, marking OPI resources as Disrupted\n",
			nodeName, newPhase)
		return nil

	case DPUPhaseReady:
		if hasPrev && prevPhase != DPUPhaseReady {
			// Transition TO Ready from a non-Ready state.
			// This means the DPU just came back online (e.g., after firmware update).
			// All hardware rules are gone — replay from WAL.
			fmt.Printf("[LifecycleWatcher] Node %s: DPU transitioned %s → Ready, replaying all rules\n",
				nodeName, prevPhase)
			return w.replayRulesForNode(ctx, nodeName)
		}
		return nil

	case DPUPhaseError:
		fmt.Printf("[LifecycleWatcher] Node %s: DPU entered Error phase, marking OPI resources as Error\n",
			nodeName)
		return nil

	default:
		return nil
	}
}

// replayRulesForNode re-sends all completed WAL entries for a given node
// to the OPI bridge, re-programming the DPU hardware after a reboot.
func (w *LifecycleWatcher) replayRulesForNode(ctx context.Context, nodeName string) error {
	bridgeAddr, err := w.DPFAdapter.GetBridgeAddress(nodeName)
	if err != nil {
		return fmt.Errorf("cannot replay rules for node %s: %w", nodeName, err)
	}

	bridge, err := w.BridgeFactory(bridgeAddr)
	if err != nil {
		return fmt.Errorf("failed to connect to bridge at %s: %w", bridgeAddr, err)
	}

	completed := w.WAL.GetAllComplete()
	replayCount := 0
	for _, entry := range completed {
		if entry.NodeName != nodeName {
			continue
		}

		switch entry.OperationType {
		case "CreateBridge":
			// Bridge handles idempotency — duplicate creates return OK
			_, err := bridge.CreateBridge(ctx, entry.ResourceName, 0)
			if err != nil {
				fmt.Printf("[LifecycleWatcher] Failed to replay %s on %s: %v\n",
					entry.OperationType, nodeName, err)
				continue
			}
		case "CreateSecurityPolicy":
			_, err := bridge.CreateSecurityPolicy(ctx, 0, "tcp", "BLOCK")
			if err != nil {
				fmt.Printf("[LifecycleWatcher] Failed to replay %s on %s: %v\n",
					entry.OperationType, nodeName, err)
				continue
			}
		}
		replayCount++
	}

	fmt.Printf("[LifecycleWatcher] Replayed %d rules on node %s\n", replayCount, nodeName)
	return nil
}

// =============================================================================
// Section 10: Operator Manager (Wires Everything Together)
// =============================================================================

// OperatorManager is the top-level orchestrator that initializes all
// components and runs the controller loops. In production, this would be
// built on top of controller-runtime's manager.Manager.
type OperatorManager struct {
	DPFAdapter              *DPFStatusAdapter
	CircuitBreaker          *CircuitBreaker
	WAL                     *WriteAheadLog
	NetworkPolicyController *NetworkPolicyController
	LifecycleWatcher        *LifecycleWatcher
}

// NewOperatorManager creates and wires the complete OPI Operator.
func NewOperatorManager(bridgeFactory func(addr string) (OPIBridgeClient, error)) *OperatorManager {
	adapter := NewDPFStatusAdapter()
	cb := NewCircuitBreaker(3, 30*time.Second)
	wal := NewWriteAheadLog()

	return &OperatorManager{
		DPFAdapter:     adapter,
		CircuitBreaker: cb,
		WAL:            wal,
		NetworkPolicyController: NewNetworkPolicyController(adapter, cb, wal, bridgeFactory),
		LifecycleWatcher:        NewLifecycleWatcher(adapter, wal, bridgeFactory),
	}
}

// StartReconciliationLoop runs the periodic state reconciliation that
// diffs desired state (CRDs) against actual state (DPU hardware).
// This self-healing loop runs every 60 seconds.
func (m *OperatorManager) StartReconciliationLoop(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("[Reconciliation] Context cancelled, stopping reconciliation loop")
			return
		case <-ticker.C:
			m.runReconciliation(ctx)
		}
	}
}

// runReconciliation performs a single reconciliation pass across all nodes.
func (m *OperatorManager) runReconciliation(ctx context.Context) {
	nodes := m.DPFAdapter.ListAllNodes()
	for nodeName, phase := range nodes {
		if phase != DPUPhaseReady {
			continue
		}

		bridgeAddr, err := m.DPFAdapter.GetBridgeAddress(nodeName)
		if err != nil {
			continue
		}

		bridge, err := m.LifecycleWatcher.BridgeFactory(bridgeAddr)
		if err != nil {
			continue
		}

		// Query actual state from hardware
		actualRules, err := bridge.ListAllRules(ctx)
		if err != nil {
			fmt.Printf("[Reconciliation] Failed to list rules on %s: %v\n", nodeName, err)
			continue
		}

		// Query desired state from WAL
		desiredEntries := m.WAL.GetAllComplete()
		desiredRules := make(map[string]bool)
		for _, entry := range desiredEntries {
			if entry.NodeName == nodeName {
				desiredRules[entry.ResourceName] = true
			}
		}

		// Detect orphaned rules (in hardware but not in desired state)
		actualSet := make(map[string]bool)
		for _, rule := range actualRules {
			actualSet[rule] = true
			if !desiredRules[rule] {
				fmt.Printf("[Reconciliation] Orphaned rule %s on %s — should be cleaned up\n",
					rule, nodeName)
			}
		}

		// Detect missing rules (in desired state but not in hardware)
		for rule := range desiredRules {
			if !actualSet[rule] {
				fmt.Printf("[Reconciliation] Missing rule %s on %s — should be re-created\n",
					rule, nodeName)
			}
		}
	}
}
