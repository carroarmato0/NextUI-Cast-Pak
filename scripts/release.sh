#!/bin/sh
set -e
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR/.."

./scripts/build.sh all

VERSION=$(grep '"version"' pak.json | sed 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/')
rm -rf dist
mkdir -p dist

for PLATFORM in tg5040 tg5050 my355; do
    PAKDIR="dist/all/Tools/$PLATFORM/Cast.pak"
    mkdir -p "$PAKDIR/bin/$PLATFORM" "$PAKDIR/lib/$PLATFORM" "$PAKDIR/assets"
    cp launch.sh pak.json "$PAKDIR/"
    cp bin/"$PLATFORM"/cast "$PAKDIR/"
    cp bin/"$PLATFORM"/ffmpeg "$PAKDIR/bin/$PLATFORM/" 2>/dev/null || true
    cp lib/"$PLATFORM"/lib*.so* "$PAKDIR/lib/$PLATFORM/" 2>/dev/null || true
    cp assets/* "$PAKDIR/assets/" 2>/dev/null || true
    chmod +x "$PAKDIR/launch.sh" "$PAKDIR/cast"
done

cd dist/all
zip -r "../Cast-$VERSION.pak.zip" .
echo "Release: dist/Cast-$VERSION.pak.zip"
