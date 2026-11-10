#!/usr/bin/env bash
# Schnelltest des modularen Installers (ohne Root, mit --root + --dry-run / echter Testroot)
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(mktemp -d /tmp/zp-install-selftest.XXXXXX)"
trap 'rm -rf "$ROOT"' EXIT

out="$("$HERE/zp-install" --list)"
echo "$out" | grep -q 'linux-server'
echo "$out" | grep -q '^  check'
# Profil dry-run (postgres ohne psql wird übersprungen)
"$HERE/zp-install" --dry-run --yes --root "$ROOT" --profile linux-server >/dev/null
# Echter Testroot ohne systemd/postgres
"$HERE/zp-install" --yes --root "$ROOT" --modules check,binary,config >/dev/null
test -x "$ROOT/usr/local/bin/zeitprotokoll-server"
test -f "$ROOT/etc/zeitprotokoll/zeitprotokoll.env"
grep -q '^ZP_SECRET_KEY=.' "$ROOT/etc/zeitprotokoll/zeitprotokoll.env"
# Uninstall dry-run
"$HERE/zp-install" --dry-run --yes --root "$ROOT" --purge --profile uninstall >/dev/null
echo "install/selftest.sh ok"
