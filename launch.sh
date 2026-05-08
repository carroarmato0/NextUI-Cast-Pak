#!/bin/sh
PAK_DIR="$(dirname "$0")"
PAK_NAME="$(basename "$PAK_DIR")"
PAK_NAME="${PAK_NAME%.*}"
# PLATFORM is always exported by NextUI; SHARED_USERDATA_PATH may or may not be set.
_base="${SHARED_USERDATA_PATH:-/mnt/SDCARD/.userdata/$PLATFORM}"
export HOME="$_base/$PAK_NAME"
unset _base

# Select bundled SDL2 libs for this device family.
if [ -d /usr/miyoo ]; then
    PLATFORM_LIB="$PAK_DIR/lib/my355"
elif grep -q "TG5050" /proc/cpuinfo 2>/dev/null; then
    PLATFORM_LIB="$PAK_DIR/lib/tg5050"
else
    PLATFORM_LIB="$PAK_DIR/lib/tg5040"
fi

NATIVE_SDL_LIB=""
for _d in /usr/trimui/lib /usr/miyoo/lib /usr/lib /usr/local/lib; do
    if [ -f "$_d/libSDL2-2.0.so.0" ]; then
        NATIVE_SDL_LIB="$_d"
        break
    fi
done
unset _d
export LD_LIBRARY_PATH="${NATIVE_SDL_LIB:+$NATIVE_SDL_LIB:}$PLATFORM_LIB:$LD_LIBRARY_PATH"
export PATH="$PAK_DIR/bin/$PLATFORM:$PAK_DIR:$PATH"
mkdir -p "$HOME" /tmp/cast/hls

# Start daemon if not already running
PID_FILE="/tmp/cast/daemon.pid"
if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    : # daemon already running
else
    PROFILE_FLAGS=""
    if [ -f "$PAK_DIR/.profile-flags" ]; then
        PROFILE_FLAGS="$(cat "$PAK_DIR/.profile-flags" | grep daemon)"
    fi
    # shellcheck disable=SC2086
    "$PAK_DIR/cast" --daemon $PROFILE_FLAGS > /tmp/cast/daemon.log 2>&1 &
fi

# Launch UI
PROFILE_FLAGS=""
if [ -f "$PAK_DIR/.profile-flags" ]; then
    PROFILE_FLAGS="$(cat "$PAK_DIR/.profile-flags" | grep -v daemon)"
fi
cd "$PAK_DIR"
# shellcheck disable=SC2086
exec "$PAK_DIR/cast" $PROFILE_FLAGS "$@"
