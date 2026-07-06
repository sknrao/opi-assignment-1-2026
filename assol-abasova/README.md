# Hands-On Assignment 1 — LLM-Assisted Architecture Design for OPI DPU Operator

## Deliverables

| File | Contents |
|---|---|
| `architecture_design.md` | Final architecture proposal, 4 Mermaid diagrams (1 component, 3 sequence), trade-off analysis of three integration patterns, failure-mode analysis, phased plan, plus Appendix A from a second prompting iteration (design defense and translation deep dive). |
| `llm_transcript.json` | Verbatim transcript of the LLM session (Claude, Anthropic) in the required `[{"role","content"}]` array format — 4 turns across two prompting rounds. |
| `feature_skeleton.go` | Bonus. Compilable skeleton of the DPF-backed NVIDIA VSP adapter. Verified with `go build` and `go vet` (Go 1.22). |

## Approach in one paragraph

Rather than asking the LLM to invent an architecture from its training data, the session first grounded the model in the *current* state of both codebases via web research (openshift/dpu-operator README and OpenShift 4.19/4.20 DPU Operator docs; NVIDIA/doca-platform architecture docs), then asked it to enumerate integration options against explicit constraints (zero changes to the OPI core, maximal DPF reuse, Kubernetes operator hygiene). A second round challenged the recommendation (Option C vs. the bridge-operator alternative) and deep-dived the hardest translation (ServiceFunctionChain → DPUDeployment). The result: an NVIDIA VSP that satisfies the OPI plugin gRPC contract outward while acting inward as a CRD translation layer onto DPF, with DPF managed as a sub-operator — composing all three patterns suggested in the assignment (adapter, sub-operator, CRD translation) at the extension seam OPI already built for vendors.

## Documented assumptions (per communication guidelines, no clarifying questions asked)

1. "OPI DPU operator" is interpreted as the operator at `github.com/openshift/dpu-operator`, which implements the OPI vendor-agnostic APIs and today ships Intel (IPU E2100, NetSec Accelerator) and Marvell (Octeon 10) vendor-specific plugins. This matches the assignment's "focuses on Intel and Marvell offload stacks."
2. NVIDIA was chosen over AMD because the assignment names the DPF operator explicitly and DPF (`github.com/NVIDIA/doca-platform`) is public with documented CRDs, enabling a grounded rather than speculative design.
3. The translation layer is designed against DPF API version `v1alpha1` (the published version as of this writing) and deliberately fails closed on unrecognized DPF API versions.
4. `feature_skeleton.go` uses only the Go standard library so it compiles standalone as a single file with a trivial `go.mod`. Comments mark exactly where `sigs.k8s.io/controller-runtime` and the generated DPF API types (`github.com/nvidia/doca-platform/api`) slot in for production.
5. The OPI VSP gRPC method names in the skeleton are representative of the contract's semantics (init, device discovery, network-function lifecycle) rather than copied verbatim from the proto, since the design intent — not proto fidelity — is the deliverable.

## Where this stops and how it would proceed

The design is complete through the phased plan in §4.8. The next concrete implementation steps would be: generating real client stubs from the OPI VSP proto, replacing the `Client` seam with controller-runtime and the vendored DPF `v1alpha1` types, authoring the `nf-wrapper` Helm chart, and standing up the golden-file conformance suite (`testdata/` SFC → expected DPF YAML) that gates future DPF version bumps.

## Verifying the bonus skeleton

```bash
go mod init example.com/nvidiavsp && go vet ./... && go build ./...
```
