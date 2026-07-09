// Package dpf holds LOCAL MIRRORS of the subset of NVIDIA DOCA Platform
// Framework (DPF) CRD fields this adapter drives, plus the GVKs used to address
// them via the dynamic/unstructured client.
//
// WHY MIRRORS, NOT AN IMPORT: DPF (github.com/NVIDIA/doca-platform) ships no
// lightweight importable api/ module and no generated clientset — importing its
// types drags OVS/argo/gRPC forks into the build (see arch §8, assumption 6).
// So the adapter addresses DPF objects as unstructured.Unstructured against the
// GVKs below, and these plain structs document/marshal the exact field subset
// we set. Each struct names the real CRD it corresponds to.
package dpf

import "k8s.io/apimachinery/pkg/runtime/schema"

// DPF API groups (as of doca-platform main, 2026-07-03). Versions are pinned
// so a served-version mismatch is detected and surfaced, not silently ignored
// (see arch §5 / §8, reason=DPFVersionUnsupported).
const (
	ProvisioningGroup = "provisioning.dpu.nvidia.com"
	SvcGroup          = "svc.dpu.nvidia.com"
	Version           = "v1alpha1"
)

// GVKs the adapter creates/patches/reads. Real CRD kinds in doca-platform.
var (
	// DPUDeploymentGVK — one object bundling provisioning + services +
	// chaining; designed to be created programmatically. Kind: DPUDeployment.
	DPUDeploymentGVK = schema.GroupVersionKind{Group: ProvisioningGroup, Version: Version, Kind: "DPUDeployment"}

	// DPUGVK — a single provisioned DPU; status.phase drives OPI readiness.
	DPUGVK = schema.GroupVersionKind{Group: ProvisioningGroup, Version: Version, Kind: "DPU"}

	// DPUServiceChainGVK — declarative service-function chain topology.
	DPUServiceChainGVK = schema.GroupVersionKind{Group: SvcGroup, Version: Version, Kind: "DPUServiceChain"}

	// DPUServiceInterfaceGVK — a chain interface / port.
	DPUServiceInterfaceGVK = schema.GroupVersionKind{Group: SvcGroup, Version: Version, Kind: "DPUServiceInterface"}

	// DPUServiceGVK — one NF workload delivered via Helm-through-ArgoCD.
	DPUServiceGVK = schema.GroupVersionKind{Group: SvcGroup, Version: Version, Kind: "DPUService"}
)

// DPUDeployment is a LOCAL MIRROR of provisioning.dpu.nvidia.com/v1alpha1
// DPUDeployment (field subset). One per DPU set; ties a DPUFlavor + node
// selector to a provisioning request.
type DPUDeployment struct {
	// DPUFlavorRef names the admin-authored DPUFlavor (BFB, NIC fw, OVS cfg).
	DPUFlavorRef string
	// NodeSelector selects the host node(s) whose DPUs to provision.
	NodeSelector map[string]string
	// Services lists NF service references composed onto the DPUs.
	Services []string
}

// DPUServiceChain is a LOCAL MIRROR of svc.dpu.nvidia.com/v1alpha1
// DPUServiceChain (field subset). Declarative analog of OPI's imperative
// CreateNetworkFunction path (bridge wiring rides in NFRequest.bridge_id).
type DPUServiceChain struct {
	// Nodes is the ordered NF hop names forming the chain.
	Nodes []string
	// InterfaceRefs names the DPUServiceInterface objects this chain wires.
	InterfaceRefs []string
}

// DPUServiceInterface is a LOCAL MIRROR of svc.dpu.nvidia.com/v1alpha1
// DPUServiceInterface (field subset): one port/attachment in a chain.
type DPUServiceInterface struct {
	// Name is the interface identifier referenced by the chain.
	Name string
	// Network is the attachment network (NAD/IPAM owned by DPF natively).
	Network string
}

// DPUService is a LOCAL MIRROR of svc.dpu.nvidia.com/v1alpha1 DPUService
// (field subset): a single NF workload delivered as a Helm release.
type DPUService struct {
	// Name is the service identifier.
	Name string
	// Image is the NF workload image (source for the Helm/ArgoCD release).
	Image string
}

// Phase values mirror DPF DPU.status.phase strings the adapter reads back.
const (
	DPUPhaseInitializing = "Initializing"
	DPUPhaseProvisioning = "Provisioning"
	DPUPhaseReady        = "Ready"
	DPUPhaseError        = "Error"
)
