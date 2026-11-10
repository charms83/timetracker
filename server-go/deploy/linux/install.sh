#!/bin/sh
# Installer für Linux (systemd). Aufruf als root im entpackten Paket:
#   ./install.sh              Erstinstallation
#   ./install.sh --upgrade    Programm austauschen (Konfiguration und Daten bleiben)
#   ./install.sh --uninstall  Dienst und Programm entfernen (Konfiguration bleibt; mit --purge auch sie)
set -eu
[ "$(id -u)" -eq 0 ] || { echo "Bitte als root ausführen." >&2; exit 1; }
HERE="$(cd "$(dirname "$0")" && pwd)"
BIN=/usr/local/bin/zeitprotokoll-server
CONF=/etc/zeitprotokoll
ENVF=$CONF/zeitprotokoll.env
WEB=/usr/share/zeitprotokoll/web
BAK=/var/lib/zeitprotokoll/backup

case "${1:-}" in
--uninstall)
  systemctl disable --now zeitprotokoll 2>/dev/null || true
  rm -f /etc/systemd/system/zeitprotokoll.service "$BIN"; rm -rf /usr/share/zeitprotokoll; systemctl daemon-reload
  [ "${2:-}" = "--purge" ] && rm -rf "$CONF" && echo "Konfiguration gelöscht (Datenbank bleibt!)."
  echo "Entfernt. Die Datenbank wurde nicht angefasst."; exit 0;;
--upgrade) UP=1;;
"") UP=0;;
*) echo "Unbekannte Option $1 (erlaubt: --upgrade, --uninstall [--purge])" >&2; exit 2;;
esac

ARCH="$(uname -m)"; case "$ARCH" in x86_64) A=amd64;; aarch64|arm64) A=arm64;; *) echo "Architektur $ARCH nicht unterstützt" >&2; exit 1;; esac
NEWBIN="$HERE/zeitprotokoll-server-linux-$A"
[ -x "$NEWBIN" ] || { echo "Programm für $A fehlt im Paket" >&2; exit 1; }

PKGVER="?"
[ -f "$HERE/VERSION" ] && PKGVER="$(cat "$HERE/VERSION")"
NEWVER="$("$NEWBIN" version 2>/dev/null | head -n1 || echo "go-$PKGVER")"

if [ "$UP" = 1 ]; then
  [ -x "$BIN" ] || { echo "Kein Installiertes Programm unter $BIN – bitte zuerst ohne --upgrade installieren." >&2; exit 1; }
  [ -f "$ENVF" ] || { echo "Keine Konfiguration $ENVF – kein Upgrade möglich." >&2; exit 1; }
  OLDVER="$("$BIN" version 2>/dev/null | head -n1 || echo "?")"
  echo "Upgrade: $OLDVER → $NEWVER"
  echo "Hinweis: Vorher Datenbank sichern (pg_dump). Migrationen laufen beim Start."
  mkdir -p "$BAK"
  TS="$(date -u +%Y%m%dT%H%M%SZ)"
  cp -a "$BIN" "$BAK/zeitprotokoll-server.$TS"
  [ -d "$WEB" ] && tar czf "$BAK/web.$TS.tar.gz" -C "$(dirname "$WEB")" "$(basename "$WEB")" 2>/dev/null || true
  echo "Sicherung: $BAK/zeitprotokoll-server.$TS"
fi

id zeitprotokoll >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin zeitprotokoll

systemctl stop zeitprotokoll 2>/dev/null || true
install -m 0755 "$NEWBIN" "$BIN"
if [ -d "$HERE/web" ]; then rm -rf "$WEB"; mkdir -p "$(dirname "$WEB")"; cp -R "$HERE/web" "$WEB"; fi
install -m 0644 "$HERE/zeitprotokoll.service" /etc/systemd/system/zeitprotokoll.service

mkdir -p "$CONF"; chmod 0750 "$CONF"; chown root:zeitprotokoll "$CONF"
if [ ! -f "$ENVF" ]; then
  KEY="$(head -c 48 /dev/urandom | base64 | tr -d '\n=+/' | head -c 48)"
  sed "s|^ZP_SECRET_KEY=.*|ZP_SECRET_KEY=$KEY|" "$HERE/zeitprotokoll.env.example" > "$ENVF"
  chmod 0640 "$ENVF"; chown root:zeitprotokoll "$ENVF"
  echo "Konfiguration angelegt: $ENVF (Schlüssel erzeugt – bitte sichern)."
  echo "Als Nächstes: PostgreSQL-Datenbank anlegen, DATABASE_URL und ZP_PUBLIC_URL (https://…) in $ENVF eintragen, dann:"
  echo "  systemctl enable --now zeitprotokoll && journalctl -u zeitprotokoll -f"
  systemctl daemon-reload; exit 0
fi
systemctl daemon-reload
if grep -q 'CHANGEME' "$ENVF"; then echo "Bitte zuerst $ENVF ausfüllen (CHANGEME), dann: systemctl enable --now zeitprotokoll"; exit 0; fi
systemctl enable zeitprotokoll >/dev/null 2>&1 || true
systemctl start zeitprotokoll
sleep 1
RUNVER="$("$BIN" version 2>/dev/null | head -n1 || echo "?")"
if [ "$UP" = 1 ]; then
  echo "Upgrade fertig: $RUNVER"
  echo "Prüfen:  set -a; . $ENVF; set +a; zeitprotokoll-server status"
  echo "Logs:    journalctl -u zeitprotokoll -n 50 --no-pager"
else
  echo "Läuft ($RUNVER). Prüfen: zeitprotokoll-server check   (Umgebung: set -a; . $ENVF; set +a)"
fi
