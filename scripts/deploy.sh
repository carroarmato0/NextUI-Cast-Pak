#!/bin/sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

if [ "${1:-}" = "--help" ] || [ "${1:-}" = "-h" ]; then
    cat <<'EOF'
Usage: deploy.sh [<sd-path>]

Deploy the release pak (dist/all/Tools/<platform>/Cast.pak/) to a device.
Run scripts/release.sh first to produce the pak directory.

Arguments:
  <none>        Push to connected ADB device via USB (requires: adb)
  <sd-path>     Copy to a mounted SD card, e.g. /run/media/user/SD

Environment:
  DEPLOY_PLATFORM=tg5040|tg5050|my355   Target platform directory on device (default: tg5040)

Examples:
  ./scripts/deploy.sh
  ./scripts/deploy.sh /run/media/user/SD
  DEPLOY_PLATFORM=my355 ./scripts/deploy.sh
EOF
    exit 0
fi

PLATFORM="${DEPLOY_PLATFORM:-tg5040}"
PAK_SRC="dist/all/Tools/$PLATFORM/Cast.pak"

if [ ! -d "$PAK_SRC" ]; then
    echo "ERROR: $PAK_SRC not found. Run scripts/release.sh first." >&2
    exit 1
fi

SD_PATH="${1:-}"

if [ -n "$SD_PATH" ]; then
    echo "==> Deploying to SD card: $SD_PATH"
    DEST="$SD_PATH/Tools/$PLATFORM/Cast.pak"
    mkdir -p "$DEST"
    cp -r "$PAK_SRC/." "$DEST/"
    echo "Deployed to $DEST"
else
    echo "==> Deploying via ADB..."
    if ! command -v adb >/dev/null 2>&1; then
        echo "ERROR: adb not found. Install android-tools (or android-platform-tools)." >&2
        exit 1
    fi
    DEVICE="$(adb devices | awk 'NR==2 {print $1}')"
    if [ -z "$DEVICE" ]; then
        echo "ERROR: no ADB device connected. Check USB cable." >&2; exit 1
    fi
    DEST="/mnt/SDCARD/Tools/$PLATFORM/Cast.pak"
    adb shell "mkdir -p $DEST"
    adb push "$PAK_SRC/." "$DEST/"

    # Kill running daemon so the next launch picks up the new binary.
    adb shell '
      PID_FILE=/tmp/cast/daemon.pid
      if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        kill "$(cat "$PID_FILE")" && echo "daemon stopped"
      fi
    '

    echo "Deployed to $DEVICE:$DEST (relaunch Cast from the device to start new daemon)"
fi
