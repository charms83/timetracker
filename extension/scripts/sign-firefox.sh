#!/usr/bin/env bash
# Signiert das Firefox-Add-on (unlisted) bei Mozilla, damit es dauerhaft installierbar ist.
#
# Vorbereitung:
#   1. Bei https://addons.mozilla.org anmelden (Mozilla-Konto).
#   2. Developer Hub -> "Tools" -> "API-Schlüssel verwalten" -> Schlüssel erzeugen.
#   3. Werte als Umgebungsvariablen setzen:
#        export AMO_JWT_ISSUER="user:12345:67"
#        export AMO_JWT_SECRET="..."
#   4. Erst ./build.sh ausführen, dann dieses Skript.
# Ergebnis: signierte .xpi in web-ext-artifacts/
set -euo pipefail
cd "$(dirname "$0")/.."
: "${AMO_JWT_ISSUER:?AMO_JWT_ISSUER fehlt}" "${AMO_JWT_SECRET:?AMO_JWT_SECRET fehlt}"
npx web-ext sign --channel=unlisted --source-dir dist/firefox \
  --api-key "$AMO_JWT_ISSUER" --api-secret "$AMO_JWT_SECRET"
