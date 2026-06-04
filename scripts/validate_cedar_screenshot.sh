#!/bin/sh
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

QUALITY="high"
DURATION="3s"
FRAMES="1"
BUFFERS="1"
OUTDIR=""
KEEP=0
PLATFORM="tg5040"

usage() {
    cat <<'EOF'
Usage: validate_cedar_screenshot.sh [--platform tg5040] [--quality high] [--duration 3s] [--frames 1] [--buffers 1] [--outdir DIR] [--keep]

Captures a framebuffer screenshot from the connected tg5040, runs Cedar in raw
H.264 mode on the same live framebuffer, decodes the Cedar output, and compares
the decoded image against the screenshot baseline.

Outputs:
  screenshot.png       Framebuffer capture decoded from /dev/fb0
  cedar-raw.h264       Raw Cedar Annex B output
  cedar-decoded.png    Cedar output decoded back to PNG
  cedar-bench.log      Cedar encoder log
  summary.txt          Human-readable result summary
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --help|-h)
            usage
            exit 0
            ;;
        --platform)
            PLATFORM="$2"
            shift 2
            ;;
        --quality)
            QUALITY="$2"
            shift 2
            ;;
        --duration)
            DURATION="$2"
            shift 2
            ;;
        --outdir)
            OUTDIR="$2"
            shift 2
            ;;
        --frames)
            FRAMES="$2"
            shift 2
            ;;
        --buffers)
            BUFFERS="$2"
            shift 2
            ;;
        --keep)
            KEEP=1
            shift
            ;;
        *)
            echo "Unknown argument: $1" >&2
            usage >&2
            exit 1
            ;;
    esac
done

command -v adb >/dev/null 2>&1 || { echo "ERROR: adb not found" >&2; exit 1; }
command -v ffmpeg >/dev/null 2>&1 || { echo "ERROR: ffmpeg not found" >&2; exit 1; }
command -v file >/dev/null 2>&1 || { echo "ERROR: file not found" >&2; exit 1; }

if [ -z "$OUTDIR" ]; then
    OUTDIR="$(mktemp -d /tmp/cedar-validate-XXXXXX)"
else
    mkdir -p "$OUTDIR"
fi

trap 'if [ "$KEEP" -ne 1 ]; then rm -rf "$OUTDIR"; fi' EXIT

if [ "$PLATFORM" != "tg5040" ]; then
    echo "ERROR: this validation flow is only configured for tg5040" >&2
    exit 1
fi

if [ ! -x bin/tg5040/cedar-bench ]; then
    echo "ERROR: bin/tg5040/cedar-bench is missing. Build it first with ./scripts/build.sh tg5040" >&2
    exit 1
fi
if ! file bin/tg5040/cedar-bench | grep -q 'ARM aarch64'; then
    echo "ERROR: bin/tg5040/cedar-bench is not an ARM64 binary. Rebuild it with GOOS=linux GOARCH=arm64 before running validation." >&2
    exit 1
fi

parse_mode() {
    adb shell 'tr -d "\r" < /sys/class/graphics/fb0/modes' 2>/dev/null | tr -d '\r\n' | grep -oE '[0-9]+x[0-9]+' | head -n 1 | sed 's/x/ /'
}

MODE="$(parse_mode || true)"
if [ -z "$MODE" ]; then
    echo "ERROR: failed to detect framebuffer mode from /sys/class/graphics/fb0/modes" >&2
    exit 1
fi

W="${MODE% *}"
H="${MODE#* }"
BPP="$(adb shell 'tr -d "\r" < /sys/class/graphics/fb0/bits_per_pixel' 2>/dev/null | tr -d '\n' | tr -cd '0-9')"
if [ -z "$BPP" ]; then
    echo "ERROR: failed to read bits_per_pixel" >&2
    exit 1
fi
if [ "$BPP" -ne 32 ]; then
    echo "ERROR: expected 32 bpp, got $BPP" >&2
    exit 1
fi

BYTES_PER_PIXEL=$((BPP / 8))
BLOCKS=$(((W * H * BYTES_PER_PIXEL) / 4096))
if [ "$BLOCKS" -le 0 ]; then
    echo "ERROR: computed invalid block count" >&2
    exit 1
fi

HOST_RAW="$OUTDIR/fb0.raw"
HOST_SCREEN="$OUTDIR/screenshot.png"
HOST_CEDAR_RAW="$OUTDIR/cedar-raw.h264"
HOST_CEDAR_PNG="$OUTDIR/cedar-decoded.png"
HOST_LOG="$OUTDIR/cedar-bench.log"
SUMMARY="$OUTDIR/summary.txt"

# Push the current cedar-bench if it isn't already present on the device.
adb push bin/tg5040/cedar-bench /tmp/cedar-bench >/dev/null
adb shell chmod +x /tmp/cedar-bench >/dev/null

# Capture the framebuffer screenshot using the same direct /dev/fb0 approach as the reference script.
adb shell "rm -f /tmp/screen.raw && dd if=/dev/fb0 bs=4096 count=$BLOCKS of=/tmp/screen.raw 2>/dev/null" >/dev/null
adb pull /tmp/screen.raw "$HOST_RAW" >/dev/null
ffmpeg -y -hide_banner -loglevel error -f rawvideo -pix_fmt bgra -s "${W}x${H}" -i "$HOST_RAW" "$HOST_SCREEN"

# Run Cedar in raw mode and dump the Annex B bitstream for isolated validation.
if [ "$FRAMES" -gt 0 ]; then
    adb shell "/tmp/cedar-bench -encoder cedar -cedar-raw -cedar-frames $FRAMES -cedar-buffers $BUFFERS -quality $QUALITY -dump /tmp/cedar-raw.h264" >"$HOST_LOG" 2>&1 || true
else
    adb shell "/tmp/cedar-bench -encoder cedar -cedar-raw -duration $DURATION -cedar-buffers $BUFFERS -quality $QUALITY -dump /tmp/cedar-raw.h264" >"$HOST_LOG" 2>&1 || true
fi
adb pull /tmp/cedar-raw.h264 "$HOST_CEDAR_RAW" >/dev/null
ffmpeg -y -hide_banner -loglevel error -fflags +genpts -i "$HOST_CEDAR_RAW" -frames:v 1 "$HOST_CEDAR_PNG" || true

if python3 - "$HOST_SCREEN" "$HOST_CEDAR_PNG" "$SUMMARY" <<'PY'
import subprocess
import sys
from pathlib import Path

screen = Path(sys.argv[1])
cedar = Path(sys.argv[2])
summary = Path(sys.argv[3])

if not cedar.exists() or cedar.stat().st_size == 0:
    summary.write_text(
        "FAIL\n\nCedar output is missing or empty.\n"
        f"screenshot={screen}\ncedar_png={cedar}\n",
        encoding="utf-8",
    )
    sys.exit(1)


def decode_png(path: Path) -> bytes:
    p = subprocess.run(
        ["ffmpeg", "-y", "-hide_banner", "-loglevel", "error", "-i", str(path), "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1"],
        check=True,
        stdout=subprocess.PIPE,
    )
    return p.stdout

screen_raw = decode_png(screen)
cedar_raw = decode_png(cedar)
if len(screen_raw) != len(cedar_raw):
    summary.write_text(
        "FAIL\n\nImage sizes differ after decode.\n"
        f"screen_bytes={len(screen_raw)}\ncedar_bytes={len(cedar_raw)}\n"
        f"screenshot={screen}\ncedar_png={cedar}\n",
        encoding="utf-8",
    )
    sys.exit(1)

# Compute per-channel mean absolute error.
count = len(screen_raw)
abs_sum = 0
max_diff = 0
for a, b in zip(screen_raw, cedar_raw):
    d = a - b if a >= b else b - a
    abs_sum += d
    if d > max_diff:
        max_diff = d
mae = abs_sum / count
# Heuristic threshold: working Cedar should be visually close to the framebuffer.
# Corrupt output will blow past this by orders of magnitude.
passed = mae <= 18.0 and max_diff <= 255
summary.write_text(
    ("PASS\n" if passed else "FAIL\n")
    + f"\nmean_abs_error={mae:.2f}\nmax_abs_error={max_diff}\n"
    + f"screenshot={screen}\ncedar_png={cedar}\n",
    encoding="utf-8",
)
if not passed:
    sys.exit(1)
PY
then
    STATUS=0
else
    STATUS=$?
fi

cat "$SUMMARY"

echo
if [ "$STATUS" -eq 0 ]; then
    echo "Artifacts:"
    echo "  $HOST_SCREEN"
    echo "  $HOST_CEDAR_RAW"
    echo "  $HOST_CEDAR_PNG"
    echo "  $HOST_LOG"
    echo "  $SUMMARY"
else
    echo "Artifacts (failed validation):"
    echo "  $HOST_SCREEN"
    echo "  $HOST_CEDAR_RAW"
    echo "  $HOST_CEDAR_PNG"
    echo "  $HOST_LOG"
    echo "  $SUMMARY"
fi

exit "$STATUS"
