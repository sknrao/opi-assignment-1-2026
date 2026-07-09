# Requirements & Constraints (≤500 words)

_Turn 1 deliverable (task 5). No architecture proposed — this scopes the design turn._

## Must preserve on the OPI side (no regression)

1. **The public API surface is frozen.** `DpuOperatorConfig` (cluster-scoped singleton `dpu-operator-config`, group `config.openshift.io/v1`) stays the top-level switch, with only its current `LogLevel` spec. `ServiceFunctionChain`, `DataProcessingUnit(Config)`, and downstream `DpuNetwork` keep their shapes. A user must be able to add NVIDIA support **without editing existing CRD schemas**.
2. **Vendor selection stays automatic via PCI autodetection.** NVIDIA must slot into the hardcoded `VendorDetector` slice (`internal/platform/vendordetector.go`) using vendor ID `15b3` + BlueField device IDs — same mechanism as Intel/Marvell. No config field, no nodeSelector-based dispatch.
3. **The VSP gRPC contract is the vendor boundary.** Whatever we build for NVIDIA must ultimately satisfy the daemon's `VendorPlugin` client: 8 RPCs across `LifeCycleService`, `DeviceService`, `BridgePortService`, `NetworkFunctionService`, `DpuNetworkConfigService`, served on the unix socket. The vendor-neutral daemon, dpu-cni, and NRI webhook must not change behavior.
4. **Zero regression for Intel/Marvell/Netsec.** Their detectors, VSP pods, image env keys, and build targets remain untouched and independently testable. Adding NVIDIA is additive.
5. **Both topologies keep working.** 1-cluster and 2-cluster (deploy-time, via kubeconfig count) must both accommodate NVIDIA.

## Can lean on DPF to solve (don't rebuild)

DPF already implements the entire NVIDIA DPU lifecycle; we should **drive it, not reimplement it**:

- **Provisioning:** BFB flash, DPUFlavor/Template, DPUSet→DPU, DMS pod, host-network daemon, reboot, kubeadm join → the DPU cluster. Reuse via `provisioning.dpu.nvidia.com` CRDs.
- **DPU-side control plane:** DPUCluster (Kamaji) **or** — crucially — `type: static` BYO-cluster via a kubeconfig Secret, which maps directly onto OPI's 2-cluster DPU-side kubeconfig.
- **Service delivery / SFC:** DPUService (Helm-via-ArgoCD), DPUServiceChain/Interface/NAD/IPAM — DPF's analog of OPI's SFC + dpu-cni path.
- **One-object orchestration:** `DPUDeployment` bundles provisioning + services + chaining; it is explicitly designed to be **created programmatically** by another controller.
- **Cross-cluster creds:** `DPUServiceCredentialRequest` issues DPU-cluster credentials.
- **Observability:** rich watchable status conditions on DPU/DPUService/DPUDeployment; `dpfctl` for debugging.

## Explicitly out of scope (this exercise)

- Modifying DPF internals or the DOCA firmware/BFB stack.
- AMD support (assignment allows NVIDIA **or** AMD; we pick NVIDIA).
- Re-implementing DPU provisioning, Kamaji, or ArgoCD delivery inside OPI.
- Production hardening: HA of the adapter, upgrade/rollback orchestration, full e2e on real BlueField hardware, security/RBAC hardening beyond a credible sketch.
- Merging upstream `opiproject` portability shims with downstream `openshift` features (we target `openshift/dpu-operator`, the active line, and note the fork lag).
- The bonus Go skeleton must **compile**, not be fully functional.

## Known integration frictions to design around

- DPF ships **no lightweight importable `api/` module** and **no generated clientset** — importing its types drags the full dependency graph (OVS, argo forks). An OPI-side component likely uses the **dynamic/unstructured client** against DPF GVKs instead of a hard Go import.
- NVIDIA may need a Marvell-`cp-agent`/Intel-`P4`-style **extra component** to bridge the daemon's VSP RPCs to DPF's declarative world.
