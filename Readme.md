# Hands-On Assignment 1: LLM-Assisted Architecture Design for OPI DPU Operator

## Objective
Design a software architecture solution, leveraging a Large Language Model (LLM), to add NVIDIA (or AMD) support to the OPI (Open Programmable Infrastructure) DPU operator. 

## Background
OPI defines vendor-neutral standards and tooling for DPUs and IPUs. The existing OPI DPU operator currently focuses on Intel and Marvell offload stacks. NVIDIA provides their own standalone DPF (DOCA Platform Framework) operator. The goal of this exercise is to design an architecture that brings NVIDIA DPU support into the unified OPI operator ecosystem while maximizing the reuse of the existing DPF operator.

## Tasks
1. **LLM Prompting & Design:** Use an LLM (e.g., ChatGPT, Claude, Gemini) to help architect a solution. You must engineer prompts that guide the LLM to design an integration pattern (e.g., adapter pattern, sub-operator, or CRD translation layer) between the OPI operator and the NVIDIA DPF operator.
2. **Architecture Alignment:** Ensure the generated solution strictly aligns with Kubernetes operator patterns and the current OPI architecture.
3. **Bonus (Implementation):** Use the LLM to generate a basic foundational Go code skeleton or a foundational feature for this integration (e.g., a reconciliation loop adapter).

## Expected Outputs (Machine-Readable Formats Only)
Please submit the following files exactly as named:
1. `architecture_design.md`
   * A Markdown document containing the final architecture proposal, sequence diagrams (Mermaid.js format), and trade-off analysis.
2. `llm_transcript.json`
   * A JSON file containing the exact prompts you used and the LLM's responses. Must follow a structured array format: `[{"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}]`.
3. `feature_skeleton.go` (Bonus)
   * A compilable (but not necessarily fully functional) Go source code file containing the basic structures and interfaces for the integration.
