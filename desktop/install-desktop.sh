#!/bin/bash
# Baut und installiert die Mac-App „Zeitprotokoll“ (Lasche + Schnellfenster).
# Die App ist nur ein kleiner Starter: Programm und Oberfläche lädt sie direkt
# aus ~/zeitprotokoll/desktop – ein „git pull“ reicht danach für Updates.
set -e
DIR="$(cd "$(dirname "$0")" && pwd)"
APP="/Applications/Zeitprotokoll.app"
ELECTRON_VERSION="${ELECTRON_VERSION:-38.2.0}"
ARCH="$(uname -m)"; [ "$ARCH" = "x86_64" ] && ARCH="x64"
command -v npx >/dev/null || { echo "✗ npx fehlt – bitte zuerst Node.js 22 installieren (brew install node@22)"; exit 1; }
[ -f "$DIR/main.js" ] || { echo "✗ $DIR/main.js fehlt"; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cat > "$TMP/package.json" <<JSON
{ "name": "zeitprotokoll-desktop", "productName": "Zeitprotokoll", "version": "1.6.0", "main": "loader.js", "private": true }
JSON
cat > "$TMP/loader.js" <<'JS'
// Lädt das eigentliche Programm aus ~/zeitprotokoll/desktop (Updates ohne Neubau der App)
const path = require("path"), fs = require("fs"), os = require("os");
const target = path.join(os.homedir(), "zeitprotokoll", "desktop", "main.js");
if (fs.existsSync(target)) require(target);
else {
  const { app, dialog } = require("electron");
  app.whenReady().then(() => { dialog.showErrorBox("Zeitprotokoll", "Programmdateien nicht gefunden:\n" + target); app.quit(); });
}
JS

echo "→ Baue Zeitprotokoll.app (Electron $ELECTRON_VERSION, $ARCH) – beim ersten Mal lädt das ca. 100 MB …"
( cd "$TMP" && npx --yes @electron/packager@18 . Zeitprotokoll --platform=darwin --arch="$ARCH" \
    --electron-version="$ELECTRON_VERSION" --out=out --overwrite --icon="$DIR/icon.icns" \
    --app-bundle-id=com.example.zeitprotokoll --extend-info="$DIR/extra.plist" >/dev/null )

echo "→ Installiere nach /Applications"
osascript -e 'quit app "Zeitprotokoll"' 2>/dev/null || true
sleep 1
rm -rf "$APP"
cp -R "$TMP/out/Zeitprotokoll-darwin-$ARCH/Zeitprotokoll.app" "$APP"
xattr -cr "$APP"
codesign --force --deep --sign - "$APP" 2>/dev/null && echo "  ✓ lokal signiert"
open "$APP"
echo "✓ Zeitprotokoll installiert – Symbol oben in der Menüleiste, Lasche am rechten Bildschirmrand (⌥⌘Z)"
