#!/bin/bash
# Stellt Zeitprotokoll.app so um, dass sie ihre Oberfläche direkt aus
# ~/zeitprotokoll/desktop lädt. Danach kommen Updates ohne Neubau der App an.
set -e
APP="/Applications/Zeitprotokoll.app"
RES="$APP/Contents/Resources"
[ -d "$APP" ] || { echo "✗ $APP nicht gefunden"; exit 1; }
echo "→ Zeitprotokoll beenden"
osascript -e 'quit app "Zeitprotokoll"' 2>/dev/null || true
sleep 2
if [ -f "$RES/app.asar" ]; then
  cp "$RES/app.asar" "$HOME/zeitprotokoll/desktop/app.asar.backup"
  rm "$RES/app.asar"
  echo "  ✓ alte Programmdatei gesichert: ~/zeitprotokoll/desktop/app.asar.backup"
fi
mkdir -p "$RES/app"
cat > "$RES/app/package.json" <<'JSON'
{ "name": "zeitprotokoll-desktop", "productName": "Zeitprotokoll", "version": "1.6.0", "main": "loader.js", "private": true }
JSON
cat > "$RES/app/loader.js" <<'JS'
// Lädt das eigentliche Programm aus ~/zeitprotokoll/desktop (Updates ohne Neubau der App)
const path = require("path"), fs = require("fs"), os = require("os");
const target = path.join(os.homedir(), "zeitprotokoll", "desktop", "main.js");
if (fs.existsSync(target)) require(target);
else {
  const { app, dialog } = require("electron");
  app.whenReady().then(() => { dialog.showErrorBox("Zeitprotokoll", "Programmdateien nicht gefunden:\n" + target); app.quit(); });
}
JS
xattr -cr "$APP"
codesign --force --deep --sign - "$APP" 2>/dev/null && echo "  ✓ lokal signiert"
echo "→ Starte Zeitprotokoll"
open "$APP"
sleep 4
echo -n "Server: "; curl -s http://localhost:3747/api/health || echo "(noch nicht erreichbar – App startet ihn gleich)"; echo
echo "✓ Fertig. Rückgängig: cd ~/zeitprotokoll/desktop && ./install-app.sh"
