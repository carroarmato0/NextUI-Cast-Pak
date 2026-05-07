#!/bin/sh
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

PLATFORM="${1:-tg5040}"
REMOTE_PATH="/mnt/SDCARD/Tools/$PLATFORM/Cast.pak"

./scripts/build.sh "$PLATFORM"

adb shell "mkdir -p $REMOTE_PATH/lib/$PLATFORM $REMOTE_PATH/bin/$PLATFORM"

adb push bin/"$PLATFORM"/cast "$REMOTE_PATH/cast"
adb shell chmod +x "$REMOTE_PATH/cast"
adb push launch.sh "$REMOTE_PATH/launch.sh"
adb shell chmod +x "$REMOTE_PATH/launch.sh"
adb push pak.json "$REMOTE_PATH/pak.json"

for so in lib/"$PLATFORM"/*.so*; do
    [ -f "$so" ] && adb push "$so" "$REMOTE_PATH/$so"
done

if [ -f bin/"$PLATFORM"/ffmpeg ]; then
    adb push bin/"$PLATFORM"/ffmpeg "$REMOTE_PATH/bin/$PLATFORM/ffmpeg"
    adb shell chmod +x "$REMOTE_PATH/bin/$PLATFORM/ffmpeg"
fi

# Kill running daemon so the next launch picks up the new binary.
adb shell '
  PID_FILE=/tmp/cast/daemon.pid
  if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    kill "$(cat "$PID_FILE")" && echo "daemon stopped"
  fi
'

echo "Deployed to $REMOTE_PATH (relaunch Cast from the device to start new daemon)"
