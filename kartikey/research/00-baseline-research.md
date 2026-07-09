# Baseline Research — OPI DPU Operator + NVIDIA DPF (Turn 1, research only)

_Date: 2026-07-03. All findings verified against local shallow clones of current `main`._

## Repo baseline (as cloned)

| Repo | HEAD | Date | Top merge |
|------|------|------|-----------|
| `opiproject/dpu-operator` (upstream) | `3092bcb` | 2026-04-28 | PR #1 `feat/upstream-k8s-portability` |
| `openshift/dpu-operator` (downstream) | `846e112` | 2026-05-07 | PR #636 `dpunetwork-controller` |
| `NVIDIA/doca-platform` (DPF) | `234e7d4` | 2026-07-02 | `render DPUFlavorTemplate into per-DPU DPUFlavors` |

**Key lineage finding:** despite the org names, `openshift/dpu-operator` is the **active/mature line** (PR #636, newest feature `DpuNetwork` lands here first) and `opiproject/dpu-operator` is a **freshly-reorganized portability fork** of it (single PR #1). *Both* `PROJECT` files declare `repo: github.com/openshift/dpu-operator`, `domain: openshift.io`. The opiproject fork adds plain-Kubernetes portability shims (non-`.rhel` Dockerfiles, `config/default-upstream*`, cert-manager NRI variant, `NRI_TLS_PROVIDER` path) but **lags**: it has 4 CRDs vs downstream's 5 (missing `DpuNetwork`), and an older `dpu-api/api.proto` (missing `DpuNetworkConfigService` + `bridge_id`). **Integration target = `openshift/dpu-operator`** (unless portability to vanilla k8s is an explicit goal).

---

## PART A — Confirm / correct the ground-truth bullets

### OPI DPU operator

| # | Bullet | Verdict | Correction / evidence |
|---|--------|---------|-----------------------|
| 1 | Kubebuilder/controller-runtime operator; `dpu-operator-controller-manager` Deployment reconciling CRDs | **CONFIRMED** | `namePrefix: dpu-operator-` + Deployment `controller-manager` = `dpu-operator-controller-manager` (`config/default/kustomization.yaml:9`, `config/manager/manager.yaml`). Manager reconciles **4–5 CRDs** + runs a validating webhook; the per-node `dpu-daemon` is a *separate binary* (`cmd/daemon`), not the manager. |
| 2 | Singleton `DpuOperatorConfig` rolls out `dpu-daemon` DaemonSet + VSP pods | **PARTLY WRONG (causal chain)** | `DpuOperatorConfig` is **Cluster-scoped** (`config/crd/bases/...dpuoperatorconfigs.yaml:15 scope: Cluster`), singleton **enforced by validating webhook** (name must == `dpu-operator-config`, `api/v1/dpuoperatorconfig_webhook.go:51-53`, `pkgs/vars/vars.go:6`). Creating it rolls out the **`dpu-daemon` DaemonSet + NRI webhook + NADs only**. VSP pods are created **one hop later**: each daemon **autodetects hardware → creates a `DataProcessingUnit` CR → the `DataProcessingUnit` controller renders the VSP pod** (`internal/controller/dataprocessingunit_controller.go:131-179`). |
| 3 | Intel + Marvell each ship own VSP; Marvell has extra control-plane agent; common internal contract the daemon calls | **CONFIRMED (+3rd vendor)** | Contract = gRPC over unix socket, `dpu-api/api.proto` + forked opi-api services, wrapped by Go `VendorPlugin` interface (`internal/daemon/plugin/vendorplugin.go:27-37`). A **third in-tree vendor** exists: **Intel Netsec Accelerator** (`intel-netsec`). Marvell's extra piece = OCTEON PCIe-EP `octep_cp_agent` (`Dockerfile.mrvlCPAgent`). Intel's analogous extra piece = the **P4 SDK** service (`--p4Image`). |
| 4 | Optional `ServiceFunctionChain` CR deploys workload pod via dedicated `dpu-cni` | **CONFIRMED** | `ServiceFunctionChain` (group `config.openshift.io/v1`, namespaced, short `sfc`). Pod created **daemon-side** by `SfcReconciler` with NAD `dpunfcni-conf` (`type: dpu-cni`). Manager-side `servicefunctionchain_controller.go` is a **no-op stub**. |
| 5 | Both 1-cluster and 2-cluster topologies | **CONFIRMED (deploy-time, not a CR field)** | Selected via Taskfile targets (`deploy-1c` vs `deploy-2-clusters`), i.e. one-vs-two kubeconfigs — **not** a code switch or CR field. Runtime side (host vs DPU) is auto-detected per node (`IsDpuPlatform` → `DataProcessingUnit.Spec.IsDpuSide` + node label `dpu.config.openshift.io/dpuside`). |
| 6 | `network-resources-injector` webhook wires DPU network resources into pods | **CONFIRMED (default-off on plain k8s)** | `MutatingWebhookConfiguration` `network-resources-injector-config` (`/mutate` on pod CREATE). **Disabled by default on vanilla Kubernetes** — deploys only on OpenShift/MicroShift or with cert-manager (`NRI_TLS_PROVIDER`, `dpuoperatorconfig_controller.go:368-390`). |

### NVIDIA DPF

| # | Bullet | Verdict | Correction / evidence |
|---|--------|---------|-----------------------|
| 1 | Helm `dpf-operator` in `dpf-operator-system`, singleton `DPFOperatorConfig` (`operator.dpu.nvidia.com/v1alpha1`) | **CONFIRMED (singleton is controller-, not webhook-, enforced)** | Chart `deploy/charts/dpf-operator`. Singleton enforced in reconcile loop (`internal/operator/controllers/dpfoperatorconfig_controller.go:56-59,171-176`; default name `dpfoperatorconfig`, ns `dpf-operator-system`); there's even a `TODO` to add creation-time validation. CR is **Namespaced**, not cluster-scoped. |
| 2 | Host Cluster / DPU Cluster split; DPU cluster = Kamaji OR "static" pre-existing cluster via kubeconfig Secret | **CONFIRMED** | `DPUCluster.spec.type` ∈ `{kamaji, static, <isv-prefix>/...}` (`api/provisioning/v1alpha1/dpucluster_types.go:31-38,81-105`). `static` → `spec.kubeconfig` names a Secret (key `super-admin.conf`); requires `DPFOperatorConfig.spec.staticClusterManager`. |
| 3 | Provisioning group: BFB, DPUFlavor, DPUSet, DPU (DMS pod + host-net daemon + kubeadm join), DPUCluster | **CONFIRMED (+ DPUFlavorTemplate, DPUDevice, DPUNode…)** | `provisioning.dpu.nvidia.com/v1alpha1`. DPU controller drives **DMS pod → flash BFB over rshim → reboot → Host Network Config daemon (VF+bridge) → kubeadm join**. DPU `Status.Phase` = 25-value enum. New: **DPUFlavorTemplate** (Go-templated flavor rendered per-DPU from `DPUDevice.spec.values`). |
| 4 | Service group: DPUService (Helm chart), DPUServiceChain/Interface/NAD/IPAM/CredentialRequest; DPUDeployment as recommended bundling object | **CONFIRMED** | `svc.dpu.nvidia.com/v1alpha1`. DPUService renders a Helm chart to the DPU cluster **via an ArgoCD Application**. DPUDeployment bundles DPUs (BFB+Flavor+DPUSets) + a `Services` map (DPUServiceTemplate + DPUServiceConfiguration) + ServiceChains; owns children via label `svc.dpu.nvidia.com/owned-by-dpudeployment`. |
| 5 | `dpfctl` prints kubectl-tree view | **CONFIRMED** | `cmd/dpfctl`; `dpfctl describe [all|dpuclusters|dpudeployments|dpuservices|dpusets|...]` builds an `ObjectTree` rooted at `DPFOperatorConfig`. Also `sosreport`. |

---

## PART B — OPI vendor extension points (task 3)

**The VSP gRPC contract is NOT the only seam.** Adding a vendor touches **five compile/build-time seams** — there is **no runtime/out-of-tree plugin path**:

1. **VSP gRPC contract** (`dpu-api/api.proto` + forked opi-api). Vendor implements a gRPC **server** on unix socket `/var/run/dpu-daemon/vendor-plugin/vendor-plugin.sock`. Daemon is the client via the `VendorPlugin` Go interface.
2. **Vendor detector registry (the dispatch point)** — hardcoded Go slice in `internal/platform/vendordetector.go:65-70` (`// add more detectors here`). Each vendor = a struct implementing `VendorDetector`. **Vendor selection = PCI autodetection** (vendor/device IDs + product strings), NOT nodeSelector/config field. NVIDIA/Mellanox `15b3` is **absent** today.
3. **Per-vendor VSP pod template** — bindata `internal/controller/bindata/vsp/<DpuPlatformName>/99.vsp-pod.yaml` (`go:embed`).
4. **Image env key** — `internal/images/images.go` `AllImageKeys()` + matching `env:` in `config/manager/manager.yaml`. Key **must equal** `DpuPlatformName()` with `-`→`_`. Image is an **operator Deployment env var**, NOT a CR field (`DpuOperatorConfigSpec` has only `LogLevel`).
5. **Per-vendor Dockerfile + build target** — `Dockerfile.<Vendor>VSP[.rhel]`, `taskfiles/`, `Makefile`.

**No CRD schema change needed to add a vendor.** Webhooks (validating on DpuOperatorConfig; mutating NRI) are generic, not vendor seams.

### OPI CRD inventory

| Group/Version | Kind | Scope | Short | Notes |
|---|---|---|---|---|
| config.openshift.io/v1 | DpuOperatorConfig | Cluster (singleton `dpu-operator-config`) | — | top-level switch |
| config.openshift.io/v1 | ServiceFunctionChain | Namespaced | sfc | optional SFC |
| config.openshift.io/v1 | DataProcessingUnit | Cluster | dpu | **created by the daemon**, not humans; drives VSP pod |
| config.openshift.io/v1 | DataProcessingUnitConfig | Namespaced | dpuconfig | |
| config.openshift.io/v1 | DpuNetwork | Cluster | dpunet | **downstream openshift ONLY** |

### The VSP contract precisely (for the design step)

- **Transport:** insecure gRPC over unix socket `/var/run/dpu-daemon/vendor-plugin/vendor-plugin.sock`.
- **Go client interface:** `VendorPlugin` (`internal/daemon/plugin/vendorplugin.go:27-37`), only concrete impl = `GrpcPlugin`.
- **8 RPCs across 5 services** the daemon actually calls:
  - `LifeCycleService.Init(InitRequest{dpu_mode,dpu_identifier}) → IpPort{ip,port}` (from forked opi-api)
  - `DeviceService.GetDevices(Empty) → DeviceListResponse`; `DeviceService.SetNumVfs(VfCount) → VfCount` (opi-api)
  - `BridgePortService.CreateBridgePort / DeleteBridgePort` (opi-api evpn-gw)
  - `NetworkFunctionService.CreateNetworkFunction / DeleteNetworkFunction(NFRequest{input,output,bridge_id})` (local `dpu-api`)
  - `DpuNetworkConfigService.SetDpuNetworkConfig(DpuNetworkConfigRequest{is_accelerated})` (local `dpu-api`, downstream-only)
  - (`HeartbeatService.Ping` is served by the DPU-side daemon manager, **not** the VSP.)
- **VSP launch:** a **per-DPU Pod** pinned via `nodeName`, rendered by `DataProcessingUnitReconciler`; shares `/var/run` hostPath with the daemon for the socket.
- **Marvell extra component** = `octep_cp_agent` (PCIe-EP mailbox control plane), installed as a host **systemd unit** by the VSP. Intel's analog = P4 SDK service. → NVIDIA may or may not need an analogous extra image depending on how BlueField exposes host PF/VF control.

**Minimal NVIDIA new-vendor checklist (current design):** (1) NVIDIA VSP gRPC server binary; (2) `nvidia-dpu.go` `VendorDetector` with `15b3` + BlueField IDs; (3) register in the detector slice; (4) image key `nvidia_dpu` in `images.go` + `manager.yaml` env; (5) `bindata/vsp/nvidia-dpu/99.vsp-pod.yaml`; (6) `Dockerfile.NvidiaVSP.rhel` + build target; (7) optional cp-agent-equivalent image. All source/build changes → operator rebuild.

---

## PART C — DPF external-driving surface (task 4)

Places clearly designed to be driven by an **external system**, not a human:

1. **DPUDeployment** — the primary "single object" API for orchestrators/GitOps; fans out into DPUSets + DPUServices + DPUServiceChains/Interfaces it owns via labels. Rich status conditions (11 sub-conditions).
2. **DPUServiceTemplate + DPUServiceConfiguration** — ISV/vendor-authored inputs consumed by the DPUDeployment controller (the DOCA-app packaging contract).
3. **DPUService** — points at a Helm chart (`oci://`/`https://`), rendered onto the DPU cluster via ArgoCD; can be GitOps- or controller-created.
4. **DPUCluster `type: static` + `spec.kubeconfig` Secret** — the **bring-your-own DPU cluster** hook. External system creates the admin-kubeconfig Secret (key `super-admin.conf`) and names it; requires `DPFOperatorConfig.spec.staticClusterManager`. **This is the cleanest seam for OPI's 2-cluster topology to hand DPF a DPU cluster.**
5. **Custom DPUCluster manager (ISV `type` prefix)** — documented pluggable contract: an external controller owns a DPUCluster of custom type, must publish an admin-kubeconfig Secret + set `.spec.kubeconfig`. Explicit "another controller drives DPF" seam.
6. **DPUServiceCredentialRequest** — cross-cluster credential issuance (ServiceAccount + kubeconfig/token Secret in a target cluster); status exposes expiry/issuedAt for rotation.
7. **Provisioning inputs created programmatically:** BFB, DPUFlavor, DPUFlavorTemplate, DPUSet (MachineSet-like).
8. **NodeEffect external gates:** `Action.Hold` (annotation `wait-for-external-nodeeffect` blocks until an external system clears it) and `Action.CustomAction` (external ConfigMap-defined pod) — let an external orchestrator arbitrate host disruption.
9. **DPFOperatorConfig toggles:** `kamajiClusterManager`, `staticClusterManager`, `provisioningController.installInterface` (gNOI/hostAgent/redfish/mock), `deploymentMode` (zero-trust|host-trusted).

**Status conditions meant to be watched:** `DPFOperatorConfig` (Ready/SystemComponentsReady…), `DPU.Status.Phase` (25-value) + Operational conditions, `DPUCluster.Status.Phase`, `DPUService` (Ready + 5 sub-conditions), `DPUDeployment` (11 conditions), `BFB.Status.Phase`, `DPUSet.Status.DPUStatistics`.

**Integration-relevant caveats:**
- **No separate importable `api/` module** — a single root `go.mod` (`github.com/nvidia/doca-platform`); importing the API types pulls the **entire heavy dep graph** (OVS, gRPC, argo forks). No generated typed clientset — DPF uses controller-runtime scheme registration. → An OPI-side importer either vendors the whole module or talks to DPF via the **dynamic/unstructured client** on the registered GVKs.
- Webhooks constrain creation on: BFB, DPUFlavor, DPUSet, DPUDevice, DPUNode, DPUDiscovery, DPUServiceIPAM, NodeSRIOVDevicePluginConfig. **None** on DPUService, DPUDeployment, DPUCluster, DPFOperatorConfig, DPUServiceCredentialRequest, DPUServiceChain/Interface/NAD (they use CEL rules + controller checks).

---

## Structural parallel (as noted in the prompt, confirmed)

OPI's **1-cluster / 2-cluster** host+DPU split and DPF's **Host Cluster / DPU Cluster** split are the same idea. OPI's 2-cluster mode (separate host + DPU kubeconfigs) maps naturally onto DPF's **DPUCluster `type: static`** BYO-cluster hook. OPI selects vendor by **PCI autodetection**; DPF is driven by **declarative CRDs (DPUDeployment) + kubeconfig Secrets**. The integration boundary is therefore: OPI detects an NVIDIA DPU → something on the OPI side must translate OPI intent into DPF CRDs and/or run a DPF-satisfying VSP.
