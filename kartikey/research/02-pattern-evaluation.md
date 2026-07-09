# Integration Pattern Evaluation (Turn 2 — design, candidate selection)

_Builds on `00-baseline-research.md` / `01-requirements-constraints.md`. No implementation yet._

## The one fact that drives every choice: an impedance mismatch

- **OPI's VSP contract is node-scoped, imperative, gRPC.** The VSP is a *per-DPU pod* (nodeName-pinned) that the vendor-neutral daemon calls with imperative RPCs (`Init`, `SetNumVfs`, `CreateBridgePort`, `CreateNetworkFunction`, `SetDpuNetworkConfig`). It assumes the DPU is *already provisioned and reachable* — `Init` merely returns the ip:port where the DPU-side datapath listens. It is a **Day-2 dataplane** agent.
- **DPF is cluster-scoped, declarative, CRD-driven.** DPF's crown jewel is **Day-0/Day-1 provisioning + cluster lifecycle**: flash BFB → reboot → host-net daemon → kubeadm-join the DPU into a DPU cluster, all orchestrated by *cluster-level* controllers reconciling CRDs, plus Day-2 services via ArgoCD.

So the two projects meet at *different layers* and *different scopes*. The whole design question reduces to: **where does the DPF-facing, cluster-scoped orchestration state live, and how does an inherently per-node VSP contract reach it without holding DPU-cluster credentials on every node?**

---

## Candidates

- **(a) VSP adapter** — an NVIDIA VSP implementing the 8-RPC contract that itself creates/owns DPF CRDs. OPI-native, but a per-node pod now owns cluster-scoped DPF objects and needs DPU-cluster creds on every node.
- **(b) Standalone CRD-translation sub-operator** — a separate cluster-scoped controller watching OPI CRDs and owning parallel DPF CRDs, mirroring status back. No VSP. Structurally matches DPF, but bypasses OPI's vendor-onboarding convention and hits a detection chicken-and-egg (without a detector, OPI never surfaces NVIDIA DPUs; the daemon's per-node flow also blocks waiting on a VSP gRPC endpoint).
- **(c) Embedded sub-operator** — OPI operator installs/vendors DPF and exposes DPF's native CRDs directly. Max literal reuse; forks the UX (NVIDIA users write DPF CRDs, others write OPI CRDs) and tightly couples OPI releases to DPF's.
- **(d) Do-nothing / side-by-side** — run both operators independently, documented. The control case.
- **(e) VSP-fronted translation controller (recommended hybrid)** — an (a)-shaped **thin VSP + detector** at the OPI boundary (so NVIDIA is a first-class autodetected vendor and the daemon's gRPC handshake is satisfied), backed by a (b)-shaped **singleton cluster controller** that actually owns the DPF CRDs and holds the DPU-cluster credentials in one place. The VSP carries no cluster creds; it signals intent (via the `DataProcessingUnit` CR / a namespaced request object) and reports DPF status back through the RPCs.

---

## Scoring (1–5, **5 = most favorable in every column**)

| Criterion (5 = best) | (a) VSP adapter | (b) Translation sub-op | (c) Embedded | (d) Side-by-side | **(e) VSP-fronted ctrl** |
|---|:--:|:--:|:--:|:--:|:--:|
| Reuse of DPF lifecycle *(top priority)* | 3 | 5 | 5 | 5* | **5** |
| Consistency w/ OPI arch & CRD conventions | 5 | 3 | 1 | 1 | **5** |
| Operational blast radius (small=better) | 3 | 4 | 2 | 5 | **4** |
| DPF cadence coupling / version-skew (loose=better) | 3 | 4 | 1 | 5 | **4** |
| Host↔DPU credential security boundary | 2 | 5 | 3 | 3 | **5** |
| Multi-tenancy & RBAC cleanliness | 2 | 4 | 2 | 3 | **4** |
| Implementation effort v1 (less=better) | 3 | 3 | 4 | 5 | **2** |
| **Total (unweighted)** | **21** | **28** | **18** | **27** | **29** |

\* (d) "reuses" DPF fully only by not integrating it at all — see note below.

### Scoring rationale (the non-obvious cells)

- **(a) reuse=3, security=2, rbac=2.** A per-node VSP *can* create DPF CRDs, but DPF's provisioning + DPU cluster are cluster singletons; N node-pods racing to own them is an impedance mismatch that pushes you toward under-driving DPF (just dataplane) or duplicating its orchestration. Worse, every DPU node's VSP would need write access to DPF objects and the DPU-cluster kubeconfig → credentials smeared across the whole fleet. Consistency=5 because it *is* the designed extension point.
- **(b) reuse=5, consistency=3.** Cluster-scoped ↔ cluster-scoped is the honest match to DPF: create a `DPUDeployment` and let DPF do 100% of the work; creds live in one controller (security=5). It loses on consistency because it sidesteps OPI's VSP/detector convention *and* has a chicken-and-egg: OPI only emits a `DataProcessingUnit` after PCI-autodetection via a VSP detector, and the daemon then expects a VSP gRPC endpoint — pure (b) leaves both unsatisfied unless it grows its own discovery.
- **(c) consistency=1, coupling=1, blast=2.** Exposing DPF's native CRDs means NVIDIA users no longer speak OPI's API — it forks the ecosystem the assignment is trying to unify. Vendoring the DPF binary pins OPI to DPF's release train (every DPF CVE/bump becomes an OPI release), and an OPI operator that lifecycle-owns DPF+Kamaji+ArgoCD has a large failure surface. Effort=4 because it's mostly packaging.
- **(d) reuse=5\*, consistency=1.** Full isolation gives it top marks on blast radius, coupling, and effort — but those are high *because it opts out of the task*. "Reuse" here is hollow: DPF runs, but nothing is integrated into OPI. It's the control that proves (a)–(e) must earn their complexity, not a real contender for the goal.
- **(e) dominates (b) cell-by-cell except effort.** It is literally (b)'s engine plus a thin (a) shim; the shim closes (b)'s two gaps (OPI-native detection + the daemon handshake) for a small marginal cost (consistency 3→5), while keeping creds in the single controller (security 5). The price is the most moving parts → effort=2.

**Read the table with the goal in mind:** the seven criteria don't include "actually delivers a unified OPI ecosystem," which is why (d) scores deceptively high. Weight *reuse of DPF* (stated top priority) and *OPI consistency*, and (e) leads cleanly, with (b) as the true runner-up.

---

## Recommendation: (e) — VSP-fronted translation controller

**Shape:** `NVIDIA VSP (thin, per-DPU) + NVIDIA VendorDetector (PCI 15b3/BlueField)` at the OPI edge → signals a **singleton `dpuf-adapter` controller** in the host cluster → the controller reconciles OPI intent into DPF CRDs (primarily one `DPUDeployment` per DPU set, plus `DPUServiceChain`/`Interface` for OPI `ServiceFunctionChain`/`DpuNetwork`) → DPF performs provisioning + services → the controller mirrors DPF status (DPU `Phase`, `DPUService` Ready) back onto the OPI `DataProcessingUnit`/`ServiceFunctionChain` status, which the VSP surfaces through its RPC replies. DPU-cluster credentials are obtained once, in the controller, via DPF's `DPUServiceCredentialRequest` / static-`DPUCluster` kubeconfig Secret — never on a node.

**To OPI, this simply *is* the vendor-plugin pattern (a):** the external contract is exactly the VSP gRPC + detector; NVIDIA looks like any other vendor and the OPI CRD surface is untouched. The only refactor of (a) is *hoisting the DPF-facing orchestration out of the node pod into a cluster singleton* — which is what makes the security, RBAC, and DPF-reuse scores flip from mediocre to strong.

### Why the runner-up (b) loses

Pure (b) is genuinely tempting and, in raw lines of code, **simpler** — one controller, no gRPC server, no detector, no per-node pod. It scores 28 to (e)'s 29, and on structural match to DPF it's excellent. It loses for two concrete reasons, not aesthetics. **First, the OPI entry point.** OPI surfaces a DPU only after its daemon PCI-autodetects it through a registered `VendorDetector` and then dials a VSP gRPC socket; with no NVIDIA detector/VSP, NVIDIA hardware is invisible to OPI and the daemon's node flow stalls — so pure (b) must re-implement device discovery that OPI already does, and still leaves the daemon's handshake unsatisfied. **Second, convention and blast isolation.** A side controller watching OPI CRDs cluster-wide is "an operator bolted next to OPI," not "NVIDIA, a supported OPI vendor"; it diverges from how Intel/Marvell/Netsec plug in and complicates the story for reviewers and for future vendors. (e) keeps DPF-shaped code exactly where (b) puts it — a cluster controller — but reaches it through OPI's front door. The thin VSP that buys all of that is genuinely thin (satisfy `Init` with the DPF-provisioned datapath endpoint; forward SFC RPCs as controller signals; no-op RPCs DPF handles declaratively), so (b)'s LOC advantage is small and its two functional gaps are real.

### Why not pure (a) (the a-vs-b question, head-on)

(a) and (e) share the same OPI-facing contract, so "recommend (a)" and "recommend (e)" are the same answer at the seam — the difference is *where cluster state lives*. Pure (a) puts DPF orchestration and DPU-cluster credentials inside the per-node VSP pod. That is the worst of both worlds: it fights DPF's cluster-scoped model (racing singletons, N-way ownership of `DPUDeployment`), and it spreads privileged DPU-cluster kubeconfigs across every DPU node (security=2, rbac=2). Would a single independent controller (b) have avoided all that? Yes — which is exactly why (e) *adopts (b)'s controller* as the engine. So the reasoning is: **take (a)'s boundary (OPI consistency, first-class vendor) and (b)'s engine (cluster-scoped reuse of DPF, one credential holder), and reject both in their pure forms** — (a) for the node-level credential/scope mismatch, (b) for the missing OPI front door.

---

## Open decisions to settle before detailed design (turn 3+)

1. **Provisioning ownership.** Does the OPI 2-cluster DPU-side cluster become DPF's DPU cluster via `DPUCluster type: static` (OPI provides the kubeconfig Secret), or does DPF's Kamaji own it? Leaning `static` to preserve OPI's topology control.
2. **Signal channel VSP→controller.** Reuse the existing `DataProcessingUnit` CR (status/annotations) vs. a small new namespaced request CR owned by the adapter. Leaning to reuse `DataProcessingUnit` to avoid new OPI API surface.
3. **DPF client strategy.** Dynamic/unstructured client against DPF GVKs (avoids importing DPF's heavy module) vs. vendoring `github.com/nvidia/doca-platform`. Leaning dynamic client (per turn-1 friction note).
