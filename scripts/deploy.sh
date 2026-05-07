#!/bin/sh
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

PLATFORM="${1:-tg5040}"
REMOTE_PATH="/mnt/SDCARD/Tools/$PLATFORM/Cast.pak"

./scripts/build.sh "$PLATFORM"

adb push bin/"$PLATFORM"/cast "$REMOTE_PATH/cast"
adb push bin/"$PLATFORM"/ffmpeg "$REMOTE_PATH/bin/$PLATFORM/ffmpeg" 2>/dev/null || true
adb push launch.sh "$REMOTE_PATH/launch.sh"
adb shell chmod +x "$REMOTE_PATH/cast" "$REMOTE_PATH/launch.sh"
echo "Deployed to $REMOTE_PATH"
