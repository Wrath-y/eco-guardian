#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOCAL_RAG_ROOT="${LOCAL_RAG_DIR:-$SCRIPT_DIR/../local-rag}"
GRAPH_ENDPOINT="${ECO_GUARDIAN_GRAPH_ENDPOINT:-http://127.0.0.1:8765}"
GRAPH_ENDPOINT="${GRAPH_ENDPOINT%/}"
RUNTIME_DIR="${ECO_GUARDIAN_RUNTIME_DIR:-$SCRIPT_DIR/.run}"
ECO_GUARDIAN_BINARY="$RUNTIME_DIR/eco-guardian"
STARTED_LOCAL_RAG=0

cleanup() {
    local exit_code=$?
    trap - EXIT
    if [ "$STARTED_LOCAL_RAG" -eq 1 ] && [ -f "$LOCAL_RAG_ROOT/stop.sh" ]; then
        bash "$LOCAL_RAG_ROOT/stop.sh" || true
    fi
    exit "$exit_code"
}

if ! command -v go >/dev/null 2>&1; then
    echo "Error: Go is required to start Eco Guardian." >&2
    exit 1
fi
if ! command -v npm >/dev/null 2>&1; then
    echo "Error: npm is required to build the Eco Guardian web UI." >&2
    exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
    echo "Error: curl is required to check local-rag health." >&2
    exit 1
fi

mkdir -p "$RUNTIME_DIR"
echo "Building Eco Guardian web UI"
npm --prefix "$SCRIPT_DIR/web" run build
echo "Building Eco Guardian"
go build -o "$ECO_GUARDIAN_BINARY" ./cmd/eco-guardian

local_rag_healthy() {
    curl --fail --silent --show-error --max-time 3 "$GRAPH_ENDPOINT/health" >/dev/null 2>&1
}

if local_rag_healthy; then
    echo "local-rag is already healthy at $GRAPH_ENDPOINT"
else
    if [ ! -f "$LOCAL_RAG_ROOT/start.sh" ]; then
        echo "Error: local-rag launcher not found at $LOCAL_RAG_ROOT/start.sh" >&2
        echo "Set LOCAL_RAG_DIR to the local-rag repository path." >&2
        exit 1
    fi
    echo "Starting local-rag from $LOCAL_RAG_ROOT"
    bash "$LOCAL_RAG_ROOT/start.sh"
    STARTED_LOCAL_RAG=1
    trap cleanup EXIT
fi

if ! local_rag_healthy; then
    echo "Error: local-rag did not become healthy at $GRAPH_ENDPOINT" >&2
    exit 1
fi

echo "Starting Eco Guardian with Graph endpoint $GRAPH_ENDPOINT"
cd "$SCRIPT_DIR"
export GIN_MODE="${GIN_MODE:-release}"
"$ECO_GUARDIAN_BINARY" --graph-endpoint "$GRAPH_ENDPOINT" "$@"
