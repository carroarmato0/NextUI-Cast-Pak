#!/bin/sh
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

PLATFORM="${PLATFORM:-tg5040}"
REMOTE_PAK="/mnt/SDCARD/Tools/$PLATFORM/Cast.pak"

case "$1" in
    logs)
        adb shell tail -f "/mnt/SDCARD/.userdata/$PLATFORM/logs/Cast.service.txt"
        ;;
    logs-ui)
        adb shell tail -f "/mnt/SDCARD/.userdata/$PLATFORM/logs/Cast.txt"
        ;;
    profile)
        case "$2" in
            cpu)
                FLAGS="--cpuprofile /tmp/cast/cast-cpu.prof"
                DAEMON_FLAGS="--daemon --cpuprofile /tmp/cast/cast-daemon-cpu.prof"
                echo "$FLAGS" > .profile-flags.ui
                echo "$DAEMON_FLAGS" > .profile-flags.daemon
                cat .profile-flags.ui .profile-flags.daemon > .profile-flags
                adb push .profile-flags "$REMOTE_PAK/.profile-flags"
                echo "CPU profiling enabled. Restart the pak."
                ;;
            pprof)
                ADDR="${3:-:6060}"
                echo "--daemon --pprof $ADDR" > .profile-flags
                adb push .profile-flags "$REMOTE_PAK/.profile-flags"
                echo "pprof daemon listening on $ADDR after next pak launch."
                ;;
            stop)
                adb shell rm -f "$REMOTE_PAK/.profile-flags"
                adb pull /tmp/cast/cast-daemon-cpu.prof . 2>/dev/null || true
                echo "Profiles pulled. Removed .profile-flags."
                ;;
            *) echo "Usage: debug.sh profile cpu|pprof [addr]|stop" >&2; exit 1 ;;
        esac
        ;;
    *) echo "Usage: debug.sh logs|logs-ui|profile <sub>" >&2; exit 1 ;;
esac
