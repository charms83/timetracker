#!/bin/bash
# Installiert Zeitprotokoll.app nach /Programme und startet es.
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
ZIP="$DIR/release/Zeitprotokoll-mac-arm64.zip"
[ -f "$ZIP" ] || { echo "✗ $ZIP fehlt"; exit 1; }
osascript -e 'quit app "Zeitprotokoll"' 2>/dev/null || true
sleep 1
rm -rf /Applications/Zeitprotokoll.app
ditto -x -k "$ZIP" /Applications
xattr -cr /Applications/Zeitprotokoll.app
codesign --force --deep --sign - /Applications/Zeitprotokoll.app 2>/dev/null && echo "✓ lokal signiert"
open /Applications/Zeitprotokoll.app
echo "✓ Zeitprotokoll installiert – Symbol oben in der Menüleiste, Schnellfenster mit ⌥⌘Z"
