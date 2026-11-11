#!/bin/zsh
# Baut das Installationspaket für Linux (amd64 und arm64): dist/zeitprotokoll-server-<version>.tar.gz + .sha256
#   scripts/package.sh [version]     (Standard: Datum YYYYMMDD)
# Die Weboberfläche (scripts/build-web.sh) wird mitgepackt, wenn web/ vorhanden ist.
set -e
cd "${0:A:h}/.."
V=${1:-$(date +%Y%m%d)}
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "")
BUILT=$(date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS="-s -w -X example/zeitprotokoll-server/internal/app.Version=${V} -X example/zeitprotokoll-server/internal/app.Commit=${COMMIT} -X example/zeitprotokoll-server/internal/app.Built=${BUILT}"
OUT=dist/zeitprotokoll-server-$V
rm -rf "$OUT"; mkdir -p "$OUT"
for A in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$A go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/zeitprotokoll-server-linux-$A" ./cmd/server
done
cp deploy/linux/install.sh deploy/linux/zeitprotokoll.service deploy/linux/zeitprotokoll.env.example "$OUT/"
[[ -d web ]] && cp -R web "$OUT/web"
cp docs/Betrieb-Migration.md docs/Betrieb-Update.md "$OUT/"
echo "$V" > "$OUT/VERSION"
echo "$COMMIT" > "$OUT/COMMIT"
echo "$BUILT" > "$OUT/BUILT"
( cd dist && tar czf "zeitprotokoll-server-$V.tar.gz" "zeitprotokoll-server-$V" && shasum -a 256 "zeitprotokoll-server-$V.tar.gz" > "zeitprotokoll-server-$V.tar.gz.sha256" )
rm -rf "$OUT"
echo "Fertig: dist/zeitprotokoll-server-$V.tar.gz  (Version $V${COMMIT:+ commit $COMMIT})"
