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

TARGET=""
while [ $# -gt 0 ]; do
    case "$1" in
        --help|-h)
            cat <<'EOF'
Usage: build.sh <target>

Targets:
  native        Build for the host machine (runs inside dev container)
  tg5040        Cross-compile for TrimUI Brick / Smart Pro (ARM64)
  tg5050        Cross-compile for TrimUI Smart Pro S (ARM64)
  my355         Cross-compile for Miyoo Flip (ARM64)
  all           Cross-compile for all three device platforms

Environment:
  CONTAINER_RUNTIME=docker|podman   Override container runtime (default: auto-detect)
EOF
            exit 0
            ;;
        *) TARGET="$1"; shift ;;
    esac
done

[ -z "$TARGET" ] && echo "Usage: build.sh native|tg5040|tg5050|my355|all" >&2 && exit 1

RUNTIME=""
GIT_COMMIT="${GIT_COMMIT:-}"
CACHE_DIR="$(pwd)/.go_cache"
if [ -z "${IN_CONTAINER:-}" ]; then
    mkdir -p "$CACHE_DIR"
    RUNTIME="${CONTAINER_RUNTIME:-$(detect_runtime)}"
    [ -z "$RUNTIME" ] && echo "ERROR: docker or podman required" >&2 && exit 1

    GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
    if [ "$GIT_COMMIT" != "unknown" ] && ! git diff --quiet 2>/dev/null; then
        GIT_COMMIT="${GIT_COMMIT}-dirty"
    fi
fi

DEV_IMAGE="cast-pak-dev"

ensure_dev_image() {
    $RUNTIME image inspect "$DEV_IMAGE" >/dev/null 2>&1 || \
        $RUNTIME build -t "$DEV_IMAGE" -f docker/Dockerfile.dev .
}

ensure_platform_image() {
    PLATFORM="$1"
    TAG="cast-pak-$PLATFORM-dev"
    $RUNTIME image inspect "$TAG" >/dev/null 2>&1 || \
        $RUNTIME build -t "$TAG" --build-arg "PLATFORM=$PLATFORM" \
            -f docker/Dockerfile.platform .
}

pak_version() {
    grep '"version"' pak.json | sed 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/'
}

build_native() {
    if [ -z "${IN_CONTAINER:-}" ]; then
        ensure_dev_image
        exec $RUNTIME run --rm \
            -v "$(pwd):/workspace" \
            -v "$CACHE_DIR:/go" \
            -w /workspace \
            -e IN_CONTAINER=1 \
            -e GOCACHE=/go/build-cache \
            -e GIT_COMMIT="$GIT_COMMIT" \
            "$DEV_IMAGE" "$0" native
    fi
    VERSION="$(pak_version)"
    COMMIT="${GIT_COMMIT:-unknown}"
    mkdir -p bin/native
    go build -ldflags "-X 'main.version=$VERSION' -X 'main.gitCommit=$COMMIT'" \
        -o bin/native/cast ./cmd/cast/
    echo "Built: bin/native/cast ($VERSION / $COMMIT)"
}

build_platform() {
    PLATFORM="$1"
    mkdir -p bin/"$PLATFORM" lib/"$PLATFORM" assets

    # Copy CA bundle so SSL_CERT_FILE works on devices without a system cert store.
    cp /etc/ssl/certs/ca-certificates.crt assets/ca-certificates.crt 2>/dev/null || true

    VERSION="$(pak_version)"
    COMMIT="${GIT_COMMIT:-unknown}"
    # CC, PKG_CONFIG_PATH, and SYSROOT are set by the LoveRetro toolchain image.
    # -a: force recompile; cached objects from a different toolchain can embed
    #     GLIBC symbols the device's libc lacks.
    # -tags netgo: use Go's built-in DNS resolver, avoiding GLIBC_2.34 deps.
    CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
        go build -a -tags netgo -buildvcs=false \
        -ldflags "-X 'main.version=$VERSION' -X 'main.gitCommit=$COMMIT'" \
        -o bin/"$PLATFORM"/cast ./cmd/cast/
    echo "Built: bin/$PLATFORM/cast ($VERSION / $COMMIT)"

    # Download static ffmpeg for ARM64.
    if [ ! -f bin/"$PLATFORM"/ffmpeg ]; then
        FFMPEG_URL="https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-arm64-static.tar.xz"
        curl -fsSL "$FFMPEG_URL" | tar -xJ --strip-components=1 -C bin/"$PLATFORM"/ \
            --wildcards '*/ffmpeg'
        chmod +x bin/"$PLATFORM"/ffmpeg
    fi

    # Bundle SDL2 libs from the LoveRetro sysroot.
    rm -f lib/"$PLATFORM"/libSDL2*.so*
    SDL2_SO=$(ls "$SYSROOT/usr/lib"/libSDL2-2.0.so.0.* 2>/dev/null | grep -v '\.so$' | head -1)
    SDL2_TTF_SO=$(ls "$SYSROOT/usr/lib"/libSDL2_ttf-2.0.so.0.* 2>/dev/null | grep -v '\.so$' | head -1)
    [ -n "$SDL2_SO" ]     && cp "$SDL2_SO"     lib/"$PLATFORM"/libSDL2-2.0.so.0
    [ -n "$SDL2_TTF_SO" ] && cp "$SDL2_TTF_SO" lib/"$PLATFORM"/libSDL2_ttf-2.0.so.0
}

case "$TARGET" in
    native)
        build_native
        ;;
    tg5040|tg5050|my355)
        if [ -z "${IN_CONTAINER:-}" ]; then
            ensure_platform_image "$TARGET"
            exec $RUNTIME run --rm \
                -v "$(pwd):/workspace" \
                -v "$CACHE_DIR:/go" \
                -w /workspace \
                -e IN_CONTAINER=1 \
                -e GOCACHE=/go/build-cache \
                -e GIT_COMMIT="$GIT_COMMIT" \
                "cast-pak-$TARGET-dev" "$0" "$TARGET"
        fi
        build_platform "$TARGET"
        ;;
    all)
        if [ -n "${IN_CONTAINER:-}" ]; then
            echo "ERROR: 'build.sh all' must be run from the host, not inside a container." >&2
            exit 1
        fi
        for p in tg5040 tg5050 my355; do
            ensure_platform_image "$p"
        done
        for p in tg5040 tg5050 my355; do
            "$0" "$p" &
        done
        wait
        ;;
    *)
        echo "Unknown target: $TARGET" >&2; exit 1
        ;;
esac
