# Apply dependency failure

## Status

`ai-balance-design-workflow` is blocked at task 1.5. Task 1.4 is complete. No AI implementation has been created.

## Verified Graph and impact baseline

- `internal/graph/projector/summary.go` constructs an immutable revision-scoped projection identity containing project ID, revision ID, config hash, projection schema/version, manifest hash, and node/edge counts.
- `internal/storage/sqlite/graph_projection_store.go` records that summary immutably and exposes exact-revision lookup through `GraphProjectionSummary`; ambiguous historical matches are rejected.
- `internal/graph/sync/verification.go` requires exact namespace, revision version, content hash, counts, readiness, and query readiness before accepting a provider Snapshot.
- `internal/graph/sync/impact.go` exposes only an optional, durable revision/graph-hash impact handoff. When no impact scheduler is installed it remains queued; it is not business fact or retrieval evidence.

Accordingly, a future AI retrieval adapter must bind requests and responses to the #8 `projector.Summary` identity and provider Snapshot identity. It must not use the optional #9 impact handoff or any suspected impact data as factual evidence.

## Missing hard dependency

The required `reproducible-simulation` baseline is only partially applied: `openspec instructions apply --change reproducible-simulation --json` reports `67/83` tasks complete. It now provides `internal/simulation` packages, but `internal/simulation/contract/ports.go` exposes only immutable revision/release admission and durable formal-run ports. `internal/app/simulation_admission.go` requires a matching FULL validation result before creating a simulation Job. This checkout has no `ProposalMaterialization` contract or pure versioned scenario/engine/evaluator/Metric port that can evaluate a sealed proposal materialization without creating a formal simulation run.

`balance-risk-assessment` remains unimplemented for this purpose: no `internal/risk` package or pure versioned comparison/structure/threshold evaluator port is present. Its remaining OpenSpec work must expose the same sealed-materialization preview boundary.

Applying this change must stop here. Creating an evaluator in `internal/ai` would violate the approved design and create a second simulator.

## Unblock

Complete `reproducible-simulation` with a pure, versioned sealed-materialization evaluator and normal post-accept handoff ports. Complete `balance-risk-assessment` with corresponding pure comparison/structure/threshold ports. Then resume this change with `/opsx:apply ai-balance-design-workflow`.
