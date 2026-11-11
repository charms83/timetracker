#!/usr/bin/env bash
# Baut dist/firefox (+ .xpi) und dist/chromium (+ .zip) aus src/, icons/ und manifest.base.json.
set -euo pipefail
cd "$(dirname "$0")"
rm -rf dist && mkdir -p dist/firefox dist/chromium
for t in firefox chromium; do
  cp src/*.js src/popup.html src/popup.css dist/$t/
  cp -r icons dist/$t/icons
done
node - <<'JS'
const fs = require("fs");
const base = JSON.parse(fs.readFileSync("manifest.base.json", "utf8"));
const ff = { ...base, background: { scripts: ["background.js"] },
  browser_specific_settings: { gecko: { id: "zeitprotokoll@example.com", strict_min_version: "128.0", data_collection_permissions: { required: ["none"] } } } };
const cr = { ...base, background: { service_worker: "background.js" } };
fs.writeFileSync("dist/firefox/manifest.json", JSON.stringify(ff, null, 2) + "\n");
fs.writeFileSync("dist/chromium/manifest.json", JSON.stringify(cr, null, 2) + "\n");
JS
# parse.js: Node-Export wird im Browser nicht gebraucht, ist aber harmlos (typeof module-Check).
(cd dist/firefox && zip -qr -X ../zeitprotokoll-firefox.xpi .)
(cd dist/chromium && zip -qr -X ../zeitprotokoll-chromium.zip .)
echo "Fertig:"; ls -1 dist
