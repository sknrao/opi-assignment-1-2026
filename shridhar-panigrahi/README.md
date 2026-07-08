# OPI internship, hands-on assignment 1

Shridhar Panigrahi

## The design in three sentences

NVIDIA BlueField-3 support arrives through the OPI DPU operator's existing
vendor mechanism (a detector and a VSP), with one twist: the VSP owns no
hardware, it answers the operator's gRPC socket by authoring NVIDIA DPF
custom resources and projecting their status back. The seam sits at the VSP
socket because the daemon's pod-attach path is synchronous and node-local,
which rules out a pure CRD-translation operator; the slow declarative work
moves to network-creation time so pod attach stays fast. A later section
adds lifecycle management of both upstream operators from a small bridge
operator, with a version matrix enforced at admission - written in response
to reviewer feedback during the review window.

If you read one file, read `architecture_design.md`. It stands alone.

## Deliverables

| File | What it is |
|---|---|
| `architecture_design.md` | Assumptions first, then the design: call-by-call gRPC-to-DPF mapping, topology, ownership rules, five sequence diagrams, failure paths, trade-offs including the costs of the chosen design, lifecycle management, roadmap, risks |
| `llm_transcript.json` | The exact LLM session, 18 messages in the required `[{"role", "content"}]` format. The prompts are the engineered part: they feed verified repo facts, correct the model against the real code, and end with an adversarial review plus the reviewer-suggested lifecycle exchanges |
| `feature_skeleton.go` | Bonus: compilable skeleton of the integration - detector, VSP adapter, pure status projection, reconcile loops, bridge operator types. `feature_skeleton_test.go` verifies the pure functions; `go.mod`/`go.sum` make it build as-is |

## What is verified, and how

Everything the files claim is machine-checked. The transcript parses as the
required structured array. All five Mermaid diagrams render with mermaid-cli
(rendered copies in `evidence/`, and GitHub renders them live inside the
document). The skeleton builds, vets and passes its tests; the build log is
in `evidence/`, and a CI workflow repeats these checks on every push.

```sh
python3 -m json.tool llm_transcript.json > /dev/null && echo transcript ok
go build ./... && go vet ./... && go test ./... && echo skeleton ok
```

## Supporting material

- `notes/research-notes.md` - my code-study notes on both upstream repos,
  written before the LLM sessions. The design is grounded in these facts.
- `evidence/` - the five rendered diagrams (PNG and SVG) and the build log.

## How the transcript reads

The design's wrong turns are left visible on purpose. The model's first
proposals did not survive contact with the actual VSP gRPC contract; the
correction is exchange 2. Exchanges 15 and 16 make the model argue against
its own design as a skeptical maintainer, and the two objections that
survived became requirements. The final two exchanges take up the reviewing
mentor's suggestion on lifecycle management; the design document's lifecycle
section is their synthesis.
