# Eco Guardian

[中文说明](README.md)

Eco Guardian is a local-first game balance planning and analysis tool, currently targeted at Windows 10/11 x64. It lets designers maintain structured configuration and use the same data for validation, versioning, impact analysis, scenario simulation, and risk review.

Each project uses `project.db` as its business source of truth. The runtime listens only on the local loopback address (`127.0.0.1`), so project data and management APIs are not exposed to the local network.

## Main capabilities

- Manage characters, skills, items, effects, tags, attributes, and their structured formulas and rules.
- Validate references, formula types and units, circular dependencies, and safety constraints.
- Save immutable configuration revisions, inspect field-level diffs, and run release gates.
- Use the optional `local-rag` Graph RAG service to analyze direct and indirect dependencies and show impact paths with evidence.
- Run reproducible simulations with fixed scenarios, versions, and random seeds, then produce balance risk reports.
- Generate evidence-backed structured AI drafts; every draft still goes through validation, simulation, and human approval and cannot be published directly.
- Back up and restore local projects, with graceful degradation when Graph or AI services are unavailable.

## Repository layout

| Path | Purpose |
| --- | --- |
| `cmd/eco-guardian` | Go application entry point |
| `internal/` | Domain model, rule validation, HTTP API, versioning, simulation, risk, backup, and runtime orchestration |
| `web/` | React + TypeScript + Ant Design + Vite frontend |
| `api/openapi.yaml` | Source of truth for the HTTP API contract and generated frontend types |
| `migrations/` | SQLite database migrations |
| `scripts/` | Build, contract-check, and packaging helpers |

Production builds embed `web/dist`, the API contract, migrations, and built-in schemas into the Go executable. `local-rag` is a separate optional Graph service; AI features require a local or cloud OpenAI-compatible provider to be configured.

## Run from source

Source builds run on Linux, macOS, and Windows. External Graph mode uses the
same loopback `local-rag` contract on all three systems; the self-contained
complete package with bundled runtimes/models remains a Windows x64 artifact.
A source build requires:

- Go 1.25 (as specified in `go.mod`);
- Node.js 22 and npm;
- Git.

From the repository root, run:

```sh
# Install frontend dependencies
npm --prefix web ci

# Generate web/dist; Go's //go:embed requires these files
npm --prefix web run build

# Start the local runtime
go run ./cmd/eco-guardian
```

When the `local-rag` repository is next to this repository, one command can start both services:

```sh
# macOS / Linux
./start.sh

# Windows CMD
start.bat
```

The launcher builds and directly runs `.run/eco-guardian`, reuses a healthy `local-rag:8765`, or invokes the neighboring repository's launcher when needed. When Eco Guardian exits, it stops only the local-rag instance that it started and leaves a pre-existing instance alone. Extra arguments are forwarded to Eco Guardian, for example `./start.sh --browser-auto-open false`. Set `LOCAL_RAG_DIR` when the repositories are not siblings.

After startup, the application binds to an available `127.0.0.1` port, opens the default browser, and prints the URL in the terminal. Press `Ctrl+C` to stop it.

Common launch options:

```sh
# Do not open the browser automatically
go run ./cmd/eco-guardian --browser-auto-open false

# Prefer a specific port; fall back to a random loopback port if it is busy
go run ./cmd/eco-guardian --preferred-port 31888

# Connect to an already-running local-rag service
go run ./cmd/eco-guardian --graph-endpoint http://127.0.0.1:9300
```

Run `go run ./cmd/eco-guardian --help` to list all launch options. If `local-rag` or an AI provider is not configured, related capabilities are shown as unavailable or degraded; local editing, validation, versioning, and backup remain available.

## Build an executable

Build the frontend first, then run the following in a Windows terminal:

```powershell
npm --prefix web ci
npm --prefix web run build
New-Item -ItemType Directory -Force dist | Out-Null
go build -o dist/eco-guardian.exe ./cmd/eco-guardian
.\dist\eco-guardian.exe
```

The repository also provides Make targets. The current Makefile invokes the repository's `rtk` command; use the explicit commands above if `rtk` is not installed:

```sh
make build       # Build the frontend and compile eco-guardian
make test        # Run Go tests
make check-contract
```

## Common checks

```sh
go test ./...
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run lint
npm --prefix web run check:api
```

macOS and Linux can be used for frontend development and most tests. The current local runtime's host-directory, browser, and credential adapters support Windows x64 only.
