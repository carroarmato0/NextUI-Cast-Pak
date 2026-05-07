#!/bin/sh
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

detect_runtime() {
    if command -v podman >/dev/null 2>&1; then echo "podman"
    elif command -v docker >/dev/null 2>&1; then echo "docker"
    else echo ""; fi
}
RUNTIME="${CONTAINER_RUNTIME:-$(detect_runtime)}"
[ -z "$RUNTIME" ] && echo "ERROR: docker or podman required" >&2 && exit 1

CACHE_DIR="$(pwd)/.go_cache"
mkdir -p "$CACHE_DIR"
$RUNTIME build -t cast-pak-dev -f docker/Dockerfile.dev . 2>/dev/null || true
$RUNTIME run --rm \
    -v "$(pwd):/workspace" \
    -v "$CACHE_DIR:/go" \
    -w /workspace \
    -e IN_CONTAINER=1 \
    -e GOCACHE=/go/build-cache \
    cast-pak-dev \
    go test -tags headless ./...
