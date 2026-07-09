# OPI & NVIDIA DPF Integration Architecture

This document defines the system architecture, design patterns, and coordination protocols for integrating the **Open Programmable Infrastructure (OPI) Operator** with the **NVIDIA DOCA Platform Framework (DPF) Operator** on Kubernetes.

---

## 1. Architectural Blueprint

The integration uses a **Hybrid Status-Driven Adapter Controller** model. This design keeps the data path (networking, storage, security) vendor-neutral via OPI gRPC, while leveraging NVIDIA's DPF for low-level DPU lifecycle management (firmware, provisioning, health).

```
┌──────────────────────────────────────────────────────────────────────┐
│                     KUBERNETES CONTROL PLANE (Host OS)               │
│                                                                      │
│  ┌──────────────────┐         ┌──────────────────────────────────┐  │
│  │  DPF CRDs        │         │  OPI CRDs                        │  │
│  │  (NVIDIA-owned)  │         │  (OPI-owned, vendor-neutral)     │  │
│  │                  │         │                                  │  │
│  │  • DPUSet        │         │  • DpuNetworkPolicy              │  │
│  │  • DPUService    │         │  • DpuStorageVolume               │  │
│  │  • DPUDeployment │         │  • DpuSecurityPolicy             │  │
│  │                  │         │  • DpuIPsecTunnel                │  │
│  └────────┬─────────┘         └──────────────┬───────────────────┘  │
│           │                                   │                      │
│      WRITES status                       WRITES status               │
│      (owner)                             (owner)                     │
│           │                                   │                      │
│           │          READS status              │                      │
│           │          (observer only)           │                      │
│           │          ┌─────────────────────────│                      │
│           │          │                         │                      │
└───────────┼──────────┼─────────────────────────┼──────────────────────┘
            │          │                         │
            ▼          ▼                         ▼
┌────────────────────────────┐     ┌──────────────────────────────────┐
│  DPF OPERATOR              │     │  OPI OPERATOR                    │
│  (NVIDIA's code)           │     │  (Your code)                     │
│                            │     │                                  │
│  Reconciles:               │     │  Contains:                       │
│  • DPUSet                  │     │  ┌────────────────────────────┐  │
│  • DPUService              │     │  │  DPF Status Adapter        │  │
│  • DPUDeployment           │     │  │  (reads DPF CRD status     │  │
│                            │     │  │   to determine readiness)  │  │
│  Manages:                  │     │  └────────────────────────────┘  │
│  • Firmware updates        │     │  ┌────────────────────────────┐  │
│  • DPU provisioning        │     │  │  OPI CRD Controllers       │  │
│  • Health monitoring       │     │  │  (reconcile OPI CRDs,      │  │
│                            │     │  │   send gRPC to bridge)     │  │
│  Does NOT know about OPI   │     │  └────────────────────────────┘  │
│                            │     │  ┌────────────────────────────┐  │
│                            │     │  │  gRPC Client               │  │
│                            │     │  │  (talks to OPI Bridge      │  │
│                            │     │  │   on DPU port 50051)       │  │
│                            │     │  └────────────────────────────┘  │
└────────────────────────────┘     └──────────────────────────────────┘
```

---

## 2. Core Design Patterns

To coordinate these two peer operators without violating the Kubernetes **"one controller, one CRD"** principle, we implement the following patterns:

### 2.1. Status-Driven Adapter Pattern (Read-Only Coordination)
The OPI Operator watches NVIDIA's `DPUSet` custom resources but **never modifies them**. Instead, it uses a cached adapter to query DPU readiness states before executing data path operations. This prevents resource version conflicts and avoids schema coupling.

### 2.2. Write-Ahead Log (WAL)
Every gRPC operation intended for the DPU's OPI bridge is logged to a local transactional store inside the OPI Operator before execution. If the operator or DPU crashes mid-operation, the WAL facilitates state recovery on restart.

### 2.3. Active State Reconciliation (Self-Healing)
Every 60 seconds, the OPI Operator diffs the desired state (defined in Kubernetes CRDs) against the active hardware configuration (queried from the DPU Bridge). Any discrepancies (missing rules or orphaned config) are corrected automatically.

---

## 3. Component Details & Interface Definitions

### 3.1. DPF Status Adapter

```go
package adapters

import (
	"context"
	"fmt"
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime/pkg"
	
	// Mock NVIDIA DPF API group
	dpfv1 "github.com/nvidia/dpf-operator/api/v1"
)

type DPUReadiness struct {
	NodeName       string
	Vendor         string    // e.g. "nvidia"
	Phase          string    // e.g. "Ready", "Provisioning", "FirmwareUpdate", "Error"
	BridgeAddr     string    // "192.168.100.2:50051"
	FWVersion      string
	LastTransition time.Time
}

type DPFStatusAdapter struct {
	client.Client
	mu       sync.RWMutex
	dpuCache map[string]*DPUReadiness
}

func (a *DPFStatusAdapter) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	dpuSet := &dpfv1.DPUSet{}
	if err := a.Get(ctx, req.NamespacedName, dpuSet); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, dpu := range dpuSet.Status.DPUs {
		a.dpuCache[dpu.NodeName] = &DPUReadiness{
			NodeName:       dpu.NodeName,
			Vendor:         "nvidia",
			Phase:          string(dpu.Phase),
			BridgeAddr:     fmt.Sprintf("%s:50051", dpu.ManagementIP),
			FWVersion:      dpu.FirmwareVersion,
			LastTransition: dpu.LastTransitionTime.Time,
		}
	}

	return ctrl.Result{}, nil
}

func (a *DPFStatusAdapter) IsDPUReady(nodeName string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	r, ok := a.dpuCache[nodeName]
	return ok && r.Phase == "Ready"
}

func (a *DPFStatusAdapter) GetBridgeAddress(nodeName string) (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	r, ok := a.dpuCache[nodeName]
	if !ok || r.Phase != "Ready" {
		return "", fmt.Errorf("dpu on node %s not ready (phase: %s)", nodeName, r.Phase)
	}
	return r.BridgeAddr, nil
}
```

### 3.2. OPI CRD Controller (DpuNetworkPolicy)

```go
package controllers

import (
	"context"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrl "sigs.k8s.io/controller-runtime/pkg"
	
	opiv1 "opi.opiproject.org/api/v1alpha1"
	opipb "github.com/opiproject/opi-api/network/v1/gen/go"
	"opi-operator/pkg/adapters"
	"opi-operator/pkg/grpc"
)

type NetworkPolicyController struct {
	client.Client
	DPFAdapter *adapters.DPFStatusAdapter
	GRPCPool   *grpc.ConnectionPool
}

func (c *NetworkPolicyController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	policy := &opiv1.DpuNetworkPolicy{}
	if err := c.Get(ctx, req.NamespacedName, policy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	nodeName := policy.Spec.NodeName

	// 1. Verify DPU status with DPF Adapter
	if !c.DPFAdapter.IsDPUReady(nodeName) {
		policy.Status.Phase = "WaitingForDPU"
		policy.Status.Message = "DPU is currently being managed/provisioned by DPF"
		_ = c.Status().Update(ctx, policy)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	bridgeAddr, err := c.DPFAdapter.GetBridgeAddress(nodeName)
	if err != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// 2. Perform transactional OPI gRPC updates
	conn, err := c.GRPCPool.Get(bridgeAddr)
	if err != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	client := opipb.NewNetInterfaceServiceClient(conn)

	resp, err := client.CreateBridge(ctx, &opipb.CreateBridgeRequest{
		Bridge: &opipb.Bridge{
			Name:   policy.Spec.BridgeName,
			VlanId: policy.Spec.VlanID,
		},
	})
	if err != nil {
		policy.Status.Phase = "Error"
		policy.Status.Message = fmt.Sprintf("gRPC error: %s", err.Error())
		_ = c.Status().Update(ctx, policy)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// 3. Mark active state
	policy.Status.Phase = "Active"
	policy.Status.Message = "Interface successfully created on BlueField-3 hardware"
	policy.Status.HardwareID = resp.Name
	_ = c.Status().Update(ctx, policy)

	return ctrl.Result{}, nil
}
```

---

## 4. Lifecycle Coordination Flow

```
 DPF Operator          K8s API Server          OPI Operator
      │                      │                      │
      │  DPUSet.status:      │                      │
      │  phase=Provisioning  │                      │
      │─────────────────────→│                      │
      │                      │  watch event          │
      │                      │─────────────────────→│
      │                      │                      │
      │                      │              DPF Adapter reads:
      │                      │              "DPU not ready"
      │                      │                      │
      │                      │              OPI CRD created by user
      │                      │                      │
      │                      │              Controller checks adapter:
      │                      │              IsDPUReady() → false
      │                      │                      │
      │                      │              Sets OPI CRD status:
      │                      │              phase=WaitingForDPU
      │                      │              Requeue after 15s
      │                      │                      │
      │  (DPF finishes       │                      │
      │   provisioning)      │                      │
      │                      │                      │
      │  DPUSet.status:      │                      │
      │  phase=Ready         │                      │
      │─────────────────────→│                      │
      │                      │  watch event          │
      │                      │─────────────────────→│
      │                      │                      │
      │                      │              DPF Adapter reads:
      │                      │              "DPU is Ready!"
      │                      │                      │
      │                      │              Lifecycle Watcher:
      │                      │              Transition to Ready
      │                      │              detected → replay rules
      │                      │                      │
      │                      │              Controller checks adapter:
      │                      │              IsDPUReady() → true
      │                      │                      │
      │                      │              Sends OPI gRPC to bridge:
      │                      │              CreateBridge(vlan=100)
      │                      │                      │──── gRPC ────→ DPU Bridge
      │                      │                      │←── OK ────────
      │                      │                      │
      │                      │              Sets OPI CRD status:
      │                      │              phase=Active ✅
```

---

## 5. Security & RBAC Scoping

To ensure complete vendor separation and avoid permission escalation:

*   The OPI Operator has **read-only** permissions on DPF resource categories.
*   The OPI Operator has **full read/write** permissions on its own CRDs.
*   The DPF Operator has **zero** visibility into OPI CRDs.

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: opi-operator-role
rules:
  # 1. Full permissions to manage OPI data path resources
  - apiGroups: ["opi.opiproject.org"]
    resources: ["dpunetworkpolicies", "dpustoragevolumes", "dpusecuritypolicies", "dpuipsectunnels"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
  - apiGroups: ["opi.opiproject.org"]
    resources: ["dpunetworkpolicies/status", "dpustoragevolumes/status", "dpusecuritypolicies/status", "dpuipsectunnels/status"]
    verbs: ["get", "update", "patch"]
    
  # 2. Read-Only permissions to watch NVIDIA DPF status (Crucial Separation)
  - apiGroups: ["svc.dpu.nvidia.com"]
    resources: ["dpusets", "dpuservices", "dpudeployments"]
    verbs: ["get", "list", "watch"]
```

---

## 6. Exception Management & Self-Healing

The OPI Operator incorporates built-in recovery routines for critical edge cases:

| Failure Event | Impact | Self-Healing / Mitigation Strategy |
|:---|:---|:---|
| **DPU hard reboots mid-operation** | DPU data path rules are wiped. | The **Lifecycle Watcher** detects node status changing from `Provisioning` -> `Ready`, and immediately triggers a full CRD state replay to reprogram the DPU. |
| **gRPC connection timeout** | Bridge is alive but slow to process requests. | All OPI bridge methods are designed to be **idempotent**. The operator retries without risk of creating duplicate hardware resources. |
| **DOCA driver panic** | Bridge becomes unresponsive. | A **Circuit Breaker** pattern opens on 3 failed gRPC calls, preventing operator starvation. The Kubernetes health probe triggers a pod restart of the DPU's OPI bridge container. |
| **Host Operator crashes** | Loss of in-memory status cache. | On startup, the OPI Operator performs a full list/watch reconciliation against the cluster CRDs and syncs the cache back with the DPU's local storage. |
| **Host OS reboot** | gRPC client disappears; DPU is still running. | The DPU keeps executing current rules (ASIC logic stays programmed). When the host returns, the operator reconnects and validates the state without traffic disruption. |
