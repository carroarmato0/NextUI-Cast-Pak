#!/bin/sh
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

detect_runtime() {
    case "${CONTAINER_RUNTIME:-}" in
        docker|podman) echo "$CONTAINER_RUNTIME"; return ;;
    esac
    if command -v podman >/dev/null 2>&1; then echo "podman"
    elif command -v docker >/dev/null 2>&1; then echo "docker"
    else echo ""; fi
}

TARGET="$1"
[ -z "$TARGET" ] && echo "Usage: build.sh native|tg5040|tg5050|my355|all" >&2 && exit 1

RUNTIME="${CONTAINER_RUNTIME:-$(detect_runtime)}"
CACHE_DIR="$(pwd)/.go_cache"
GIT_COMMIT="${GIT_COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
[ "$(git diff --quiet 2>/dev/null; echo $?)" != "0" ] && GIT_COMMIT="${GIT_COMMIT}-dirty"

pak_version() { grep '"version"' pak.json | sed 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'; }

ensure_image() {
    TAG="$1"; FILE="$2"; ARG="${3:-}"
    $RUNTIME image inspect "$TAG" >/dev/null 2>&1 || \
        $RUNTIME build -t "$TAG" ${ARG:+--build-arg "$ARG"} -f "$FILE" .
}

build_native() {
    if [ -z "${IN_CONTAINER:-}" ]; then
        mkdir -p "$CACHE_DIR"
        ensure_image cast-pak-dev docker/Dockerfile.dev
        exec $RUNTIME run --rm -v "$(pwd):/workspace" -v "$CACHE_DIR:/go" \
            -w /workspace -e IN_CONTAINER=1 -e GOCACHE=/go/build-cache \
            -e GIT_COMMIT="$GIT_COMMIT" cast-pak-dev "$0" native
    fi
    VERSION="$(pak_version)"
    mkdir -p bin/native
    go build -ldflags "-X 'main.version=$VERSION' -X 'main.gitCommit=$GIT_COMMIT'" \
        -o bin/native/cast ./cmd/cast/
    echo "Built: bin/native/cast ($VERSION / $GIT_COMMIT)"
}

build_platform() {
    PLATFORM="$1"
    if [ -z "${IN_CONTAINER:-}" ]; then
        mkdir -p "$CACHE_DIR"
        ensure_image "cast-pak-$PLATFORM-dev" docker/Dockerfile.platform "PLATFORM=$PLATFORM"
        exec $RUNTIME run --rm -v "$(pwd):/workspace" -v "$CACHE_DIR:/go" \
            -w /workspace -e IN_CONTAINER=1 -e GOCACHE=/go/build-cache \
            -e GIT_COMMIT="$GIT_COMMIT" "cast-pak-$PLATFORM-dev" "$0" "$PLATFORM"
    fi
    VERSION="$(pak_version)"
    mkdir -p bin/"$PLATFORM" lib/"$PLATFORM"
    CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
        go build -a -tags netgo -buildvcs=false \
        -ldflags "-X 'main.version=$VERSION' -X 'main.gitCommit=$GIT_COMMIT'" \
        -o bin/"$PLATFORM"/cast ./cmd/cast/
    # Copy ffmpeg from container PATH
    cp "$(which ffmpeg)" bin/"$PLATFORM"/ffmpeg 2>/dev/null || true
    # Bundle SDL2 libs
    rm -f lib/"$PLATFORM"/lib*.so*
    for lib in libSDL2-2.0.so.0 libSDL2_image-2.0.so.0 libSDL2_ttf-2.0.so.0 libSDL2_gfx-1.0.so.0; do
        SO=$(ls "$SYSROOT/usr/lib/${lib}".* 2>/dev/null | grep -v '\.so$' | head -1)
        [ -n "$SO" ] && cp "$SO" "lib/$PLATFORM/$lib"
    done
    echo "Built: bin/$PLATFORM/cast ($VERSION / $GIT_COMMIT)"
}

case "$TARGET" in
    native) build_native ;;
    tg5040|tg5050|my355) build_platform "$TARGET" ;;
    all)
        for p in tg5040 tg5050 my355; do
            ensure_image "cast-pak-$p-dev" docker/Dockerfile.platform "PLATFORM=$p"
        done
        for p in tg5040 tg5050 my355; do
            "$0" "$p" &
        done
        wait
        ;;
    *) echo "Unknown target: $TARGET" >&2; exit 1 ;;
esac
