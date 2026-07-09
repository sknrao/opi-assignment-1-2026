# OPI DPU Operator - NVIDIA DPF Integration Architecture

**LFX 2026 Mentorship - Assignment 1**  
**Candidate:** Kartikey Gupta  
**Submission Date:** July 5, 2026

---

## Overview

This repository contains my submission for the LFX 2026 Assignment 1: designing an architecture to integrate NVIDIA DPU support into the OPI (Open Programmable Infrastructure) DPU Operator by maximizing reuse of NVIDIA's existing DOCA Platform Framework (DPF).

The proposed solution is **Pattern (e): VSP-fronted Translation Controller** - a thin NVIDIA VSP + PCI detector at the OPI edge, backed by a singleton `dpuf-adapter` controller that owns all DPF CRDs and holds DPU-cluster credentials in one place.

---

## Required Deliverables

✅ **`architecture_design.md`** (74KB, 586 lines)
   - Complete architecture proposal with TL;DR and hero diagram
   - **4 Mermaid diagrams** (1 component + 3 sequence diagrams)
   - **13 sections (§1-§13)** including:
     - Executive summary and component diagram
     - CRD/API mapping table
     - Status/condition propagation design
     - Security/RBAC analysis
     - Trade-off analysis (5 patterns scored)
     - Self-review from OPI maintainer perspective (§9)
     - Storage boundary analysis (§10)
     - Definition of Done with concrete examples (§11)
     - Self-review from DPF maintainer perspective (§12)
     - Open questions for OPI maintainers (§13)

✅ **`llm_transcript.json`** (65KB, 18 messages)
   - Structured design exploration showing iterative refinement
   - Valid JSON with strict user/assistant alternation
   - Documents: research → pattern evaluation → detailed design → code generation → multiple critique passes

✅ **`feature_skeleton.go`** (30KB, entry point)
   - Compilable Kubebuilder-structured controller-runtime module
   - Two reconcilers: `ServiceFunctionChainReconciler` and `DataProcessingUnitReconciler`
   - Includes vendor scoping, version-drift classification, finalizer-based GC
   - **Bonus: 4 passing unit tests** in `reconcile_test.go`

---

## Supporting Files

To meet the "compilable" requirement:

### Core Package Files
- **`fleet.go`** - Fleet abstraction, vendor scoping, `ClusterResolver`
- **`status.go`** - DPF-phase → OPI condition mapping
- **`reconcile_test.go`** - 4 unit tests (lifecycle, SFC translation, finalizer GC, schema-drift)

### Module Structure
- **`go.mod` / `go.sum`** - controller-runtime v0.17.3
- **`api/opi/v1/`** - OPI CRD type mirrors
- **`internal/dpf/`** - DPF CRD mirrors + GVKs
- **`internal/translate/`** - OPI → DPF translation
- **`internal/vsp/`** - 7-RPC VendorPlugin + NVIDIA VSP
- **`cmd/main.go`** - Manager wiring

### Research Documentation
- **`research/`** - Three markdown files:
  - `00-baseline-research.md` - Repository exploration
  - `01-requirements-constraints.md` - Requirements analysis
  - `02-pattern-evaluation.md` - Pattern comparison

---

## Build Verification

```bash
go build ./...    # ✓ PASS
go vet ./...      # ✓ PASS  
go test ./...     # ✓ PASS (4 tests)
gofmt -l .        # ✓ clean
```

**Toolchain:** go1.25.4 darwin/arm64  
**Dependencies:** controller-runtime v0.17.3, k8s.io/apimachinery v0.29.2

---

## Approach Highlights

### 1. Systematic Exploration
- 5 architectural patterns evaluated against 7 criteria
- Scored trade-off analysis (see §7 in architecture_design.md)
- 13 documented assumptions with justification

### 2. Verification Against Real Systems
- Verified OPI CRDs via `gh` against `openshift/dpu-operator`
- Corrected VSP RPC contract against upstream `dpu-api/api.proto` (7 RPCs across 5 services)
- Used real `DataProcessingUnit` field names from upstream
- Real BlueField-3 PCI device IDs from pci.ids database

### 3. Self-Skepticism and Iteration
- **Dual self-critique:**
  - §9: Six questions from OPI maintainer perspective (found and fixed 4 bugs)
  - §12: Four questions from DPF maintainer perspective (found and fixed 2 bugs)
  - §13: Five open questions for upstream working group
- 18-message LLM transcript showing iterative refinement
- Research files documenting exploration process

### 4. Professional Engineering
- Unit tests with assertions (4 tests, all pass)
- Finalizer-based garbage collection
- Loud failure on version/schema drift
- Explicit build verification

### 5. Honest Limitations
- Storage boundary explicitly defined (§10)
- Out-of-scope items clearly stated
- No hardware testing (documented why)
- Assumptions documented and justified

---

## Architecture Summary

**Pattern (e): VSP-fronted Translation Controller**

**Key Decisions:**
- Thin NVIDIA VSP + PCI detector at OPI edge
- Singleton `dpuf-adapter` controller in host cluster
- One fleet-level `DPUDeployment` (DPF's DPUSet fans out to nodes)
- Vendor scoping on `spec.dpuProductName`
- Dynamic/unstructured DPF client (no heavy Go import)
- Adapter writes only `DPFReady` condition (single-writer rule)
- Finalizer-based GC (cross-namespace ownerRef invalid)
- Loud version-drift classification
- Multi-fleet-ready via ClusterResolver abstraction
- Host Trusted deployment model

**Why Pattern (e):**
- Maximal DPF reuse (100% unmodified)
- Native to OPI (uses existing VSP + VendorDetector extension point)
- Security boundary (node pods hold no DPU-cluster credentials)
- Isolation (vendor scoping prevents cross-vendor issues)
- Selected from 5 alternatives via scored comparison (§7)

---

## Definition of Done

Target `kubectl get dpu` output showing NVIDIA BlueField-3 reaching Ready status:

```
NAME                    DPU PRODUCT              DPU SIDE   NODE NAME       STATUS
worker-3-marvell-dpu    Marvell CN10K            true       worker-3        True
worker-4-bf3-host       NVIDIA BlueField-3       false      worker-4        True  # ← This design
worker-4-bf3-dpu        NVIDIA BlueField-3       true       worker-4        True  # ← This design
```

---

## Contact

**Candidate:** Kartikey Gupta  
**GitHub:** [@kartikeyg0104](https://github.com/kartikeyg0104)
