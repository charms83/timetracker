#!/bin/zsh
# Baut das Installationspaket für Linux (amd64 und arm64): dist/zeitprotokoll-server-<version>.tar.gz + .sha256
#   scripts/package.sh [version]     (Standard: Datum)
# Die Weboberfläche (scripts/build-web.sh) wird mitgepackt, wenn web/ vorhanden ist.
set -e
cd "${0:A:h}/.."
V=${1:-$(date +%Y%m%d)}
OUT=dist/zeitprotokoll-server-$V
rm -rf "$OUT"; mkdir -p "$OUT"
for A in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$A go build -trimpath -ldflags "-s -w" -o "$OUT/zeitprotokoll-server-linux-$A" ./cmd/server
done
cp deploy/linux/install.sh deploy/linux/zeitprotokoll.service deploy/linux/zeitprotokoll.env.example "$OUT/"
# Modularer Installer (Auswahldialoge) – parallel zum stillen install.sh
mkdir -p "$OUT/install"
cp -R install/lib install/modules install/profiles install/zp-install install/README.md "$OUT/install/"
chmod +x "$OUT/install/zp-install"
[[ -d web ]] && cp -R web "$OUT/web"
cp docs/Betrieb-Migration.md "$OUT/"
printf '%s\n' "Stiller Installer: ./install.sh" "Modular (Menü): sudo ./install/zp-install" > "$OUT/INSTALL.txt"
( cd dist && tar czf "zeitprotokoll-server-$V.tar.gz" "zeitprotokoll-server-$V" && shasum -a 256 "zeitprotokoll-server-$V.tar.gz" > "zeitprotokoll-server-$V.tar.gz.sha256" )
rm -rf "$OUT"
echo "Fertig: dist/zeitprotokoll-server-$V.tar.gz"
