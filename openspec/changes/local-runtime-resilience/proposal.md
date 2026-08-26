## Why

Eco Guardian currently has no runnable local host, dependency supervision, or truthful degradation boundary, so the planned editing and analysis capabilities cannot be started or recovered safely on a clean Windows machine. This change establishes the single-entry local runtime and keeps offline deterministic work usable when Graph, retrieval, or AI dependencies fail.

## What Changes

- Add `eco-guardian.exe` as the only user-facing entry point: load simple machine settings, choose a free loopback port, start the embedded HTTP/UI host, reopen the recent project when safe, and open the browser or print a copyable local URL when browser launch fails.
- Define complete and lightweight Windows x64 distributions. The complete Graph package includes a pinned compatible local-rag executable, Python runtime, Embedding/Rerank models, and defaults; both packages embed the Web frontend, migrations, OpenAPI, schemas, and deterministic templates, while neither bundles an LLM.
- Add owned-child supervision for bundled local-rag with hidden windows, readiness deadlines, bounded restart behavior, ordered shutdown, and a Windows Job Object. Processes not started by this Eco Guardian instance are detected and consumed but never terminated.
- Add startup compatibility negotiation against local-rag `/health`, based primarily on API `v1`, Graph Snapshot schema `1.0`, required capabilities and limits, with service SemVer used for diagnostics and a pinned minimum-fix floor rather than as the sole compatibility gate.
- Add a single runtime status projection and UI experience for Graph core, BM25/Vector/Rerank, AI Provider, release dependencies, owned-process state, degradation reasons, safe recovery actions, reconnect, log location, and manual retries.
- Preserve editing, validation, revision/candidate work, deterministic simulation, and backup capabilities during external dependency failures; disable only Graph-dependent, retrieval-dependent, AI, and formal-release actions according to their actual capability requirements.
- Coordinate startup/project-open recovery for the existing six-state Eco Guardian Jobs and four-state local-rag Tasks: replay only proven-idempotent deterministic work, reconcile accepted external Task IDs, never auto-repeat an interrupted AI Provider call, and expose recovery-required states instead of guessing success.
- Store machine settings, recent projects, runtime ownership records, diagnostics, and rotating structured logs under `%LOCALAPPDATA%/EcoGuardian`; keep all project business facts in `project.db` and keep secrets and business payloads out of settings, logs, and runtime diagnostics.
- Verify port conflicts, missing package assets/models, incompatible or timed-out services, partial capability recovery, browser failure, crash/restart, process ownership, log redaction/rotation, clean shutdown, Windows Server x64 CI behavior, and clean Windows 11 x64 startup.

## Capabilities

### New Capabilities

- `local-runtime-resilience`: Single-entry Windows local runtime, owned dependency supervision, capability negotiation, selective degradation, Job reconciliation, runtime status/configuration, diagnostics, and complete/lightweight packaging behavior.

### Modified Capabilities

None.

## Impact

- Adds planned implementation areas under `cmd/eco-guardian`, `internal/app/runtime` and Windows platform adapters, and extends the existing application composition, Graph/AI adapters, unified Job worker, HTTP/OpenAPI surface, and Vue runtime/settings UI.
- Adds `GET /api/v1/runtime/status` and the non-sensitive runtime portion of `GET/PATCH /api/v1/settings`; reuses existing Job GET/SSE/cancel, Graph status/retry, and credential endpoints rather than defining parallel protocols.
- Adds Windows release assembly and smoke-test inputs for complete Graph and lightweight packages; the complete package consumes a pinned local-rag distribution and models but does not modify local-rag or bundle an AI LLM.
- Depends on the applied boundaries from changes #5–#12 and the read-only local-rag #4 health/task/rebuild contract for full integration, while the minimal launcher/runtime skeleton can be implemented before all business modules are present.
- Does not add macOS/Linux release artifacts, remote/non-loopback serving, authentication or multi-user operation, automatic termination of user-owned processes, a remote operations plane, or a new business-fact store.
