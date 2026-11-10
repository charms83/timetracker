#!/bin/zsh
# Baut die bestehende Weboberfläche (frontend/) für den Go-Server nach server-go/web/ – berührt frontend/dist (Node-System) NICHT.
set -e
cd "${0:A:h}/../../frontend"
npx vite build --outDir ../server-go/web --emptyOutDir
echo "Fertig: server-go/web (scripts/dev.sh neu starten)"
