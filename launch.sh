#!/bin/sh
PAK_DIR="$(dirname "$0")"
PAK_NAME="$(basename "$PAK_DIR")"
PAK_NAME="${PAK_NAME%.*}"
export HOME="$SHARED_USERDATA_PATH/$PAK_NAME"

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
export SSL_CERT_FILE="$PAK_DIR/assets/ca-certificates.crt"
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
    "$PAK_DIR/cast" --daemon $PROFILE_FLAGS &
fi

# Launch UI
PROFILE_FLAGS=""
if [ -f "$PAK_DIR/.profile-flags" ]; then
    PROFILE_FLAGS="$(cat "$PAK_DIR/.profile-flags" | grep -v daemon)"
fi
cd "$PAK_DIR"
# shellcheck disable=SC2086
exec "$PAK_DIR/cast" $PROFILE_FLAGS "$@"
