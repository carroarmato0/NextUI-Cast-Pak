#!/bin/sh
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

PLATFORM="${DEPLOY_PLATFORM:-tg5040}"
PAK_DEST="/mnt/SDCARD/Tools/$PLATFORM/Cast.pak"
LOG_UI="/mnt/SDCARD/.userdata/$PLATFORM/logs/Cast.txt"
LOG_SVC="/mnt/SDCARD/.userdata/$PLATFORM/logs/Cast.service.txt"
PROF_DIR="$(pwd)/debug-profiles"

check_adb() {
    if ! command -v adb >/dev/null 2>&1; then
        echo "ERROR: adb not found. Install android-tools." >&2; exit 1
    fi
    if ! adb devices | grep -q "device$"; then
        echo "ERROR: no ADB device. Check USB cable." >&2; exit 1
    fi
}

CMD="${1:-}"
case "$CMD" in
    --help|-h)
        cat <<'EOF'
Usage: debug.sh <command>

Commands:
  logs             Stream the daemon log from the last startup (Ctrl-C to stop)
  logs-ui          Stream the UI log from the last startup (Ctrl-C to stop)
  push             Build for DEPLOY_PLATFORM and push all files via ADB
  run              Build, push, then launch directly (shows stdout/stderr)
  pull-log         Pull both log files to the current directory
  shell            Open an interactive ADB shell on the device

  profile          Deploy CPU+memory profiling build; launch via NextUI to record
  profile-cpu      Deploy CPU-only profiling build
  profile-mem      Deploy memory-only profiling build
  profile-live     Deploy live pprof build (HTTP :6060 via ADB forward)
  profile-restore  Remove profiling flags; restores normal launch behaviour
  pull-profile     Pull recorded profile files to ./debug-profiles/

Environment:
  DEPLOY_PLATFORM=tg5040|tg5050|my355   Target platform (default: tg5040)

Log paths on device:
  Daemon: /mnt/SDCARD/.userdata/<platform>/logs/Cast.service.txt
  UI:     /mnt/SDCARD/.userdata/<platform>/logs/Cast.txt

Profiling workflow:
  1. ./scripts/debug.sh profile           # deploy; read printed instructions
  2. Launch Cast via NextUI normally
  3. Use the app, then exit via B button
  4. ./scripts/debug.sh pull-profile      # fetch profiles to ./debug-profiles/
  5. ./scripts/debug.sh profile-restore   # remove flags; restores normal launch
  6. go tool pprof bin/tg5040/cast ./debug-profiles/cast-cpu.prof

Live pprof workflow:
  1. ./scripts/debug.sh profile-live      # deploy + ADB port forward
  2. Launch Cast via NextUI normally
  3. go tool pprof 'http://localhost:6060/debug/pprof/profile?seconds=30'
  4. ./scripts/debug.sh profile-restore

Examples:
  ./scripts/debug.sh logs
  ./scripts/debug.sh push
  ./scripts/debug.sh run
  DEPLOY_PLATFORM=my355 ./scripts/debug.sh push
  ./scripts/debug.sh pull-log
EOF
        exit 0
        ;;

    logs|logs-ui)
        check_adb
        if [ "$CMD" = "logs-ui" ]; then
            LOG_PATH="$LOG_UI"
            SENTINEL="cast Cast.txt starting"
        else
            LOG_PATH="$LOG_SVC"
            SENTINEL="cast Cast.service.txt starting"
        fi
        echo "==> Streaming log from last startup (Ctrl-C to stop)..."
        LINE=$(adb shell "grep -n '$SENTINEL' $LOG_PATH 2>/dev/null | tail -1 | cut -d: -f1" 2>/dev/null | tr -d '\r')
        if [ -n "$LINE" ]; then
            adb shell "tail -n +$LINE -f $LOG_PATH" | awk -v sent="$SENTINEL" '
                BEGIN { show = 0 }
                index($0, sent) { show = 1; print; next }
                show { print }
            '
        else
            adb shell "tail -f $LOG_PATH"
        fi
        ;;

    push)
        check_adb
        echo "==> Building and pushing $PLATFORM..."
        ./scripts/deploy.sh "$PLATFORM"
        ;;

    run)
        check_adb
        echo "==> Building and pushing..."
        ./scripts/deploy.sh "$PLATFORM"
        echo "==> Running (Ctrl-C to stop)..."
        adb shell "cd $PAK_DEST && PLATFORM=$PLATFORM HOME=/mnt/SDCARD/.userdata/$PLATFORM/Cast ./launch.sh 2>&1"
        ;;

    pull-log)
        check_adb
        adb pull "$LOG_SVC" ./Cast.service.txt 2>/dev/null && \
            echo "Daemon log -> $(pwd)/Cast.service.txt" || \
            echo "No daemon log on device yet"
        adb pull "$LOG_UI" ./Cast.txt 2>/dev/null && \
            echo "UI log     -> $(pwd)/Cast.txt" || \
            echo "No UI log on device yet"
        ;;

    shell)
        check_adb
        adb shell
        ;;

    profile)
        check_adb
        ./scripts/deploy.sh "$PLATFORM"
        adb shell "printf '%s' '--cpuprofile /tmp/cast/cast-cpu.prof --memprofile /tmp/cast/cast-mem.prof' > $PAK_DEST/.profile-flags"
        echo ""
        echo "Profiling build ready. Next steps:"
        echo "  1. Launch Cast via NextUI normally (no ADB needed)"
        echo "  2. Use the app, then exit via the B button"
        echo "  3. Run: ./scripts/debug.sh pull-profile"
        echo "  4. Run: ./scripts/debug.sh profile-restore"
        ;;

    profile-cpu)
        check_adb
        ./scripts/deploy.sh "$PLATFORM"
        adb shell "printf '%s' '--cpuprofile /tmp/cast/cast-cpu.prof' > $PAK_DEST/.profile-flags"
        echo ""
        echo "CPU profiling ready. Launch Cast via NextUI, exit, then pull-profile."
        ;;

    profile-mem)
        check_adb
        ./scripts/deploy.sh "$PLATFORM"
        adb shell "printf '%s' '--memprofile /tmp/cast/cast-mem.prof' > $PAK_DEST/.profile-flags"
        echo ""
        echo "Memory profiling ready. Launch Cast via NextUI, exit, then pull-profile."
        ;;

    profile-live)
        check_adb
        ./scripts/deploy.sh "$PLATFORM"
        adb shell "printf '%s' '--pprof :6060' > $PAK_DEST/.profile-flags"
        adb forward tcp:6060 tcp:6060
        echo ""
        echo "Live pprof ready (port 6060 forwarded). Launch Cast via NextUI, then:"
        echo "  go tool pprof 'http://localhost:6060/debug/pprof/profile?seconds=30'"
        echo "  go tool pprof http://localhost:6060/debug/pprof/heap"
        echo "Run ./scripts/debug.sh profile-restore when done."
        ;;

    profile-restore)
        check_adb
        adb shell "rm -f $PAK_DEST/.profile-flags"
        adb forward --remove tcp:6060 2>/dev/null || true
        echo "Profiling flags removed. App will run normally on next launch."
        ;;

    pull-profile)
        check_adb
        mkdir -p "$PROF_DIR"
        GOT=0
        adb pull /tmp/cast/cast-cpu.prof "$PROF_DIR/cast-cpu.prof" 2>/dev/null && \
            echo "CPU profile    -> $PROF_DIR/cast-cpu.prof" && GOT=1 || \
            echo "No CPU profile on device"
        adb pull /tmp/cast/cast-mem.prof "$PROF_DIR/cast-mem.prof" 2>/dev/null && \
            echo "Memory profile -> $PROF_DIR/cast-mem.prof" && GOT=1 || \
            echo "No memory profile on device"
        if [ "$GOT" -eq 1 ]; then
            echo ""
            echo "Analyze with:"
            echo "  go tool pprof bin/$PLATFORM/cast $PROF_DIR/cast-cpu.prof"
        fi
        ;;

    *)
        echo "Usage: debug.sh <command>  (--help for full list)" >&2
        echo "Commands: logs logs-ui push run pull-log shell" >&2
        echo "          profile profile-cpu profile-mem profile-live profile-restore pull-profile" >&2
        exit 1
        ;;
esac
