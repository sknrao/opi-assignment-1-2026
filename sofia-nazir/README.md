# OPI DPU Operator - NVIDIA DPF Adapter
<img width="1536" height="1024" alt="image" src="https://github.com/user-attachments/assets/32847cc0-ea6a-4a52-aa85-fa1d14c428eb" />
This project designs and implements an LLM-assisted adapter architecture to add NVIDIA BlueField DPU support to the OPI DPU Operator. It uses a vendor-neutral DPUCluster resource and translates it into NVIDIA DPF resources (DPUSet and DPUService) through an adapter layer. The solution follows Kubernetes operator patterns, reuses the existing NVIDIA DPF operator, includes a basic Go reconciliation skeleton, unit tests, a demo application, and architecture documentation generated with LLM assistance.

##

## Demo (Added example so that anyone can understand how things are working)
```bash
go run ./examples
```
<img width="935" height="480" alt="Screenshot (3386)" src="https://github.com/user-attachments/assets/7b85094c-0c9b-431c-8038-d318045d7778" />

## This demo prints the input OPI spec and the generated NVIDIA DPF objects.

## Table of Contents

- [What I Did](#what-i-did)
- [Why This Design](#why-this-design)
- [Repository Layout](#repository-layout)
- [Architecture Overview](#architecture-overview)
- [Key Features](#key-features)
- [How It Works](#how-it-works)
- [Design Process & LLM Prompting](#design-process--llm-prompting)
- [Demo](#demo)
- [Tests](#tests)
- [Local Verification](#local-verification)

This project is a small proof-of-concept operator that adds NVIDIA support to an OPI-based DPU workflow.
It uses an adapter layer to translate an OPI DPUCluster into NVIDIA DPF resources such as DPUSet and DPUService.

## NVIDIA DOCA Platform Framework (DPF) and BlueField Reuse

This adapter adds NVIDIA BlueField support by reusing the upstream NVIDIA DOCA Platform Framework (DPF) ( https://github.com/NVIDIA/doca-platform/ ) instead of re-implementing DPU provisioning from scratch.
The OPI-facing DPUCluster resource remains vendor-neutral, but the adapter translates it into NVIDIA DPF custom resources such as DPUSet and DPUService.
The reconciler then creates or updates those resources, and the actual BlueField lifecycle work is delegated to the NVIDIA DPF operator that watches those CRs.

This keeps the OPI layer simple and portable while still leveraging NVIDIA's existing platform workflow for firmware bootstrapping, device onboarding, and service orchestration.

## What I Did

I added NVIDIA support to the OPI operator by building a simple translation layer.
Instead of writing NVIDIA logic directly inside the OPI code, I used an adapter so the main OPI flow stays clean and vendor-neutral.
The controller now takes an OPI DPUCluster, translates it into NVIDIA resources, creates or updates them, and reflects the status back to the parent object.

## Why This Design

The main idea is to keep the OPI layer simple while reusing NVIDIA's existing DPF workflow instead of rebuilding everything from scratch.
This makes the code easier to understand and easier to extend later for other vendors.

## Repository Layout

| File/Folder | Purpose |
|---|---|
| api/v1alpha1 | Defines the OPI API types used by the operator. |
| controllers | Contains the reconciliation logic for creating and updating NVIDIA resources. |
| pkg/adapter | Holds the adapter and translation code for mapping OPI input to NVIDIA DPF objects. |
| examples | Includes a demo program that shows the translation flow in a simple way. |
| main.go | Starts the controller manager and registers the reconciler. |

## Architecture Overview

The architecture follows a simple adapter-based flow:

1. OPI provides a vendor-neutral input resource called DPUCluster.
2. The adapter translates that input into NVIDIA-specific DPF resources.
3. The reconciler creates or updates those resources in the cluster.
4. The status of the child resources is reflected back to the OPI object.

This keeps the OPI layer clean while reusing NVIDIA's native DPF workflow.

```text
[ Cluster Admin ]
        │
        ▼
  Apply OPI DPU Cluster
        │
        ▼
OPI DPU Operator Core
        │
        ▼
 NVIDIA DPF Adapter
        │
        ▼
 NVIDIA DPF Operator
        │
        ▼
  Provision DPU Resources
```

## Key Features

- Vendor-aware reconciliation for NVIDIA.
- Adapter-based translation from OPI to NVIDIA DPF resources.
- Clean separation between OPI logic and vendor-specific logic.
- Simple demo flow for understanding and presentation.
- Basic status propagation from child resources to parent status.

## How It Works

1. A user creates an OPI DPUCluster resource.
2. The reconciler checks the vendor and identifies it as NVIDIA.
3. The adapter converts the OPI object into NVIDIA DPF resources.
4. The controller creates or updates those resources.
5. Status from the NVIDIA resources is reflected back to the OPI resource.

## Examples & Notes

- **Controller:** The reconciler reads values from the `DPUCluster` CR (`BFB`, `DpuFlavor`, `NodeSelector`, `NetworkOffloadMode`, etc.) and dynamically generates NVIDIA `DPUSet` and `DPUService`.
- **VendorAdapter:** The reconciler uses a `VendorAdapter` interface so NVIDIA logic is separated from the main OPI controller and can be extended for AMD or other vendors.
- **Example/demo:** The example in `examples/demo_run.go` uses sample data only for demonstration; the real controller reads Kubernetes resources in-cluster (see `controllers/opicluster_reconciler.go`).

## Design Process & LLM Prompting

This project was developed step by step by first understanding the OPI design, then identifying the NVIDIA DPF resources that should be mapped, and finally implementing a small adapter layer.
The process focused on keeping the solution simple, maintainable, and close to the real operator pattern.
The LLM prompting approach was used to break the task into smaller parts such as:

- understand the OPI resource model,
- inspect NVIDIA DPF types,
- design an adapter-based approach,
- implement reconciliation helpers,
- verify behavior with tests and demo output.

## Demo

A simple demo is available in the examples folder.
It shows how an OPI DPUCluster is converted into NVIDIA resources.

Run the demo:

```bash
go run ./examples
```
<img width="935" height="480" alt="Screenshot (3386)" src="https://github.com/user-attachments/assets/7b85094c-0c9b-431c-8038-d318045d7778" />

## This demo prints the input OPI spec and the generated NVIDIA DPF objects.

## Tests

Run the full test suite:

```bash
go test ./...
```
