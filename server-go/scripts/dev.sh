#!/bin/zsh
# Testumgebung auf diesem Mac – ohne OpenShift, ohne Container: PostgreSQL (Homebrew) + Server als normales Programm.
#   scripts/dev.sh                 baut und startet den Server (Vordergrund, Strg+C beendet)
#   scripts/dev.sh <befehl> …      führt einen Server-Befehl gegen dieselbe Datenbank aus, z. B.
#                                  scripts/dev.sh status | check | user add-local anna anna@x.de user | ldap show
# Eigene Testdatenbank „zeitprotokoll_dev“; der Schlüssel liegt in ~/.zeitprotokoll-dev-key (wird einmal erzeugt, nie ausgegeben).
# Derselbe Schlüssel gehört später auf den Server (ZP_SECRET_KEY), wenn die Datenbank dorthin umzieht (docs/Betrieb-Migration.md).
set -e
cd "${0:A:h}/.."
DB=${ZP_DEV_DB:-zeitprotokoll_dev}
PORT=${ZP_DEV_PORT:-8090}
KEYFILE=$HOME/.zeitprotokoll-dev-key

brew services start postgresql@16 >/dev/null 2>&1 || true
for i in {1..30}; do pg_isready -q && break; sleep 1; done
pg_isready -q || { echo "PostgreSQL läuft nicht (brew services list)"; exit 1; }
psql -d postgres -lqt | cut -d'|' -f1 | grep -qw "$DB" || createdb "$DB"
[[ -f $KEYFILE ]] || { umask 077; head -c 48 /dev/urandom | base64 > $KEYFILE; }

export DATABASE_URL="postgres://$USER@localhost:5432/$DB?sslmode=disable"
export ZP_SECRET_KEY="$(cat $KEYFILE)"
export ZP_ADDR=":$PORT" ZP_PUBLIC_URL="http://localhost:$PORT" ZP_ALLOW_HTTP=1
export ZP_DATA_DIR="$PWD/data"
# Weboberfläche (aus ../frontend gebaut nach web/, siehe scripts/build-web.sh): wird ausgeliefert, wenn vorhanden
[[ -f web/index.html ]] && export ZP_STATIC_DIR="$PWD/web"

mkdir -p bin
go build -o bin/server ./cmd/server
if [[ $# -gt 0 ]]; then exec bin/server "$@"; fi
echo "Server: http://localhost:$PORT   Admin: /admin   Einrichtung: /setup (Token steht gleich im Log)"
exec bin/server serve
