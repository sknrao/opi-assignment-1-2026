# OPI internship, hands-on assignment 1

Shridhar Panigrahi

Architecture design for adding NVIDIA BlueField-3 support to the OPI DPU
operator by reusing the DPF (DOCA Platform Framework) operator.

## Deliverables

| File | What it is |
|---|---|
| `architecture_design.md` | The proposal: assumptions, design, call-by-call mapping, sequence diagrams (Mermaid), trade-off analysis, roadmap, risks |
| `llm_transcript.json` | The exact LLM session, 18 messages in the required `[{"role", "content"}]` format. The prompt sequence was engineered deliberately; the design document's last section explains how |
| `feature_skeleton.go` | Bonus: compilable Go skeleton of the integration (detector, VSP adapter, status projection, reconcile loop, bridge operator types). `go.mod`/`go.sum` are included so it builds as-is; `feature_skeleton_test.go` verifies the pure functions |

## Supporting material

- `notes/research-notes.md` - my code-study notes on both repos, written before
  the LLM sessions. The design is grounded in these verified facts.
- `evidence/` - all five sequence diagrams rendered with mermaid-cli (PNG and
  SVG), and the build log for the skeleton.

## How to verify

```sh
python3 -m json.tool llm_transcript.json > /dev/null && echo transcript ok
go build ./... && go vet ./... && go test ./... && echo skeleton ok
```

The Mermaid diagrams render directly in GitHub's view of
`architecture_design.md`; the files in `evidence/` are the same diagrams
rendered offline.

## Reading order

`architecture_design.md` stands alone. The transcript shows how the design
got there, including the wrong turns: the model's first-pass proposals did not
survive contact with the actual VSP gRPC contract, and the correction is left
visible in exchange 2. Exchanges 15 and 16 are an adversarial review; its two
strongest objections were folded back into the design as requirements rather
than answered defensively. The last two exchanges take up the reviewing
mentor's suggestion to explore lifecycle management of both operators from
the bridge operator; the design document's lifecycle section is their result.
