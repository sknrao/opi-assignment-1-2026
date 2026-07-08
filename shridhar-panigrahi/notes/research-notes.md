# Research notes - Assignment 1 (NVIDIA DPF support in the OPI DPU operator)

Everything below was verified directly against source code cloned on 2026-07-02:

- `research/dpu-operator` = github.com/openshift/dpu-operator (shallow clone, main)
- `research/opi-dpu-operator` = github.com/opiproject/dpu-operator (shallow clone, main)
- `research/doca-platform` = github.com/NVIDIA/doca-platform (shallow clone, main)

## 1. The "OPI DPU operator" - what it actually is

- `opiproject/dpu-operator` is an upstream-portability variant of `openshift/dpu-operator`.
  File-level diff shows the opiproject copy adds non-RHEL Dockerfiles and
  `config/default-upstream*` kustomizations (upstream k8s + cert-manager), and lags
  slightly (it is missing the newer `DpuNetwork` CRD that openshift main has).
  Its closed PRs (#1 "Feat/upstream k8s portability", #2/#3 "Sync with downstream")
  confirm the relationship. Assumption to document in the submission: "the OPI DPU
  operator" = this codebase, in its opiproject upstream form.
- README: "adds support for DPUs in a vendor-agnostic way, using (soon to be)
  standard APIs" - the standard APIs are OPI APIs (see section 3).
- Vendors supported today (in code, `internal/platform/vendordetector.go`):
  Intel IPU E2100, Marvell DPU, Intel NetSec accelerator. The detector list ends
  with the comment `// add more detectors here`. There is **zero** occurrence of
  nvidia/mellanox/bluefield/15b3 in non-vendored Go code - NVIDIA support is
  genuinely greenfield.

### CRDs (group `config.openshift.io/v1`, defined in `api/v1/`)

| Kind | Purpose (from types + controllers) |
|---|---|
| `DpuOperatorConfig` | singleton config (logLevel); its controller deploys the daemon DaemonSet + network resources injector |
| `DataProcessingUnit` | one per detected DPU (spec: dpuProductName, isDpuSide, nodeName; status: conditions). Created by the daemon's detector, reconciled by a controller that renders the vendor's VSP pod |
| `ServiceFunctionChain` | list of NetworkFunctions (name + image) with nodeSelector |
| `DpuNetwork` (openshift only, newer) | nodeSelector + dpuSelector + isAccelerated -> status.resourceName + selectedVFs (device-plugin resource per network) |

### Runtime components

- **Operator** (`cmd/main.go`, `internal/controller/`): reconciles the CRDs above.
  `dataprocessingunit_controller.go` renders per-vendor VSP pods from templates in
  `internal/controller/bindata/vsp/<vendor-dir>/99.vsp-pod.yaml`; VSP image comes from
  an env-var-driven ImageManager (`internal/images/images.go`: `intel_ipu`,
  `marvell_dpu`, `intel_netsec`, ...). Shared VSP ServiceAccount/RBAC in `bindata/vsp/shared/`.
- **Daemon** (DaemonSet on all nodes, host side and DPU side; `internal/daemon/`):
  - `DpuDetectorManager.DetectAll()` scans PCI devices (ghw) on the host side, or DMI
    product name on the DPU side; for each hit it creates a `DataProcessingUnit` CR
    and a `GrpcPlugin` handle.
  - `HostSideManager` / `DpuSideManager` own the node-local flows: device plugin
    (VFs as k8s resources), CNI command handlers (pod add/del), SR-IOV VF setup,
    SFC reconciler (DPU side), heartbeat pings to the VSP.
- **VSP (Vendor Specific Plugin)**: privileged, hostNetwork pod per DPU per node,
  launched by the operator, serving gRPC on a **unix socket** under `/var/run`
  (see `bindata/vsp/marvell-dpu/99.vsp-pod.yaml`, `pathManager.VendorPluginSocket()`).
  Reference implementations live in `internal/daemon/vendor-specific-plugins/`
  (marvell, intel-netsec, mock-vsp).

### The VSP contract (the key integration surface)

`internal/daemon/plugin/vendorplugin.go` - interface `VendorPlugin`:
Start, Close, CreateBridgePort, DeleteBridgePort, CreateNetworkFunction,
DeleteNetworkFunction, GetDevices, SetNumVfs, SetDpuNetworkConfig.

Wire protocol = two layers of gRPC services on the same socket:
1. `dpu-api/api.proto` (package `Vendor`): `LifeCycleService.Init(dpu_mode,
   dpu_identifier) -> IpPort`, `DpuNetworkConfigService.SetDpuNetworkConfig(is_accelerated)`,
   `NetworkFunctionService.Create/DeleteNetworkFunction(input, output, bridge_id)`,
   `DeviceService.GetDevices/SetNumVfs`, `HeartbeatService.Ping`.
2. **Official OPI APIs** imported from `github.com/opiproject/opi-api`:
   `network/evpn-gw/v1alpha1` `BridgePortService` (Create/DeleteBridgePort) and
   `v1/lifecycle/v1alpha1`. This is the "architecture alignment" hook: VSPs already
   speak OPI-standard gRPC.

Call flow (verified in `hostsidemanager.go`/`dpusidemanager.go`): pod requests a
DPU resource -> kubelet device plugin -> CNI add -> daemon handler -> VSP gRPC
(CreateBridgePort on host side / CreateNetworkFunction for SFC on DPU side) ->
vendor stack programs the datapath.

### Vendor extension recipe (what adding NVIDIA means mechanically)

1. New `VendorDetector` in `internal/platform/` (PCI vendor Mellanox 0x15b3,
   BlueField-3 product; DPU-side detection via DMI/product) added to the list in
   `NewDpuDetectorManager`.
2. New VSP image + pod template `bindata/vsp/nvidia-bf3/99.vsp-pod.yaml` + image
   env plumbing in `internal/images`.
3. The VSP binary itself: implements the gRPC services above. **This is where all
   the design freedom lives - and where DPF reuse happens.**

## 2. NVIDIA DPF (doca-platform) - what we must reuse

- Purpose: provision + orchestrate BlueField-3 DPUs at fleet scale.
  **Dual-cluster architecture**: host cluster (all DPF controllers) + a dedicated
  "DPU cluster" control plane (Kamaji tenant control plane pods hosted in the host
  cluster; DPU ARM nodes join it via kubeadm).
- Install: helm (`deploy/charts/dpf-operator`), driven by `DPFOperatorConfig` CR.
- API groups (`api/`, group suffix `.dpu.nvidia.com`, 30+ CRDs):
  - `provisioning`: `BFB` (BlueField bootstream image download), `DPUSet` -> `DPU`
    (per-device provisioning state machine), `DPUCluster` (Kamaji control plane),
    `DPUFlavor` (hw config incl. OVS), `DPUNode`, `DPUDevice`, `DPUDiscovery`, ...
  - `dpuservice`: `DPUService` (helm chart -> ArgoCD Application -> DPU cluster),
    `DPUDeployment` (bundle of DPUSets+DPUServices), `DPUServiceChain`,
    `DPUServiceInterface`, `DPUServiceIPAM`, `DPUServiceNAD`, and the synced
    node-level `ServiceChain(Set)`/`ServiceInterface(Set)`.
  - plus `operator`, `storage`, `vpc`, `noderesources`.
- Provisioning flow (docs/public/developer-guides/architecture/component-description.md):
  NFD labels nodes -> BFB downloaded -> DPUSet creates DPU per matching node ->
  DMS pod flashes BFB over rshim -> DPU (and optionally host) reboots -> host network
  config daemon creates VFs + host<->DPU bridge -> DPU ARM node kubeadm-joins DPUCluster.
- Service flow: `DPUService` -> ArgoCD app -> helm chart lands on DPU cluster.
- Service chains: `DPUServiceInterface/Chain/IPAM` on host cluster -> synced Sets on
  DPU cluster -> node controllers program **OVS ports and flows** (OVS-DOCA, hw
  offload); SFC CNI attaches pods; NVIPAM allocates IPs; OVN-Kubernetes offload
  ships as a DPUService.

## 3. The design problem, precisely stated

The OPI operator's vendor socket is small, imperative, and node-local.
DPF is large, declarative, and cluster-scoped (with its own second control plane).
Bringing NVIDIA into OPI "while maximizing reuse of the existing DPF operator"
means deciding where the seam goes. Overlaps that create ownership conflicts:

| Concern | dpu-operator today | DPF |
|---|---|---|
| DPU detection | daemon VendorDetector (PCI/DMI) | NFD + DPUDiscovery/DPUDevice |
| DPU provisioning | none (assumes provisioned DPU) | BFB + DPUSet/DPU + DMS flash + reboot |
| DPU k8s topology | DPU nodes run daemon+VSP (e.g. microshift) | dedicated Kamaji DPU cluster |
| VF lifecycle | VSP SetNumVfs + device plugin | host network config + SR-IOV DP + DPUFlavor |
| Service chaining | ServiceFunctionChain CR (pod images) | DPUServiceChain/Interface (OVS flows/ports) + DPUService (workloads) |
| Datapath programming | VSP CreateBridgePort/NetworkFunction | node ServiceInterface/Chain controllers on OVS-DOCA |

Candidate integration patterns for the trade-off analysis:

1. **Thin NVIDIA VSP, no DPF** - implement DOCA/OVS calls directly in a VSP.
   Rejected: zero DPF reuse, reimplements provisioning + chaining, huge maintenance.
2. **Pure CRD translation layer (bridge sub-operator)** - controller maps OPI CRs
   to DPF CRs. Clean K8s pattern, but alone it leaves the daemon's VSP socket
   unanswered (CNI/device-plugin path breaks), so it cannot be the whole story.
3. **NVIDIA VSP as an adapter/facade over DPF (+ small translation controller)** -
   the likely proposal. The VSP implements the OPI gRPC contract; instead of
   touching hardware it creates/watches DPF CRs (and, for node-local datapath
   calls, DPF's ServiceInterface/ServiceChain objects). Cluster-scoped lifecycle
   (BFB/DPUSet provisioning, DPUService) is mediated by a small
   nvidia-integration controller reconciling OPI CRs -> DPF CRs with status
   back-propagation. DPF remains fully in charge of hardware; OPI remains the
   single user-facing API. Maximum reuse, clear ownership.
4. **Side-by-side, no integration** / **merge DPF upstream into OPI** - bookend
   options for the trade-off table (no unified API vs. unrealistic coupling).

Open design questions to resolve (and document as assumptions):
- Cluster topology reconciliation: does the OPI daemon's DPU side run inside DPF's
  Kamaji DPU cluster (as a DPUService!) or do we accept host-side-only integration
  for phase 1? (Elegant idea: deploy the OPI DPU-side daemon itself as a DPUService.)
- Who wins on VF counts and detection when both stacks observe the same hardware.
- Status/condition propagation DPF -> OPI CRs (Ready conditions).
- Version skew policy against DPF's fast-moving CRD surface (30+ CRDs, v1alpha1).
