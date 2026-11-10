#!/bin/bash
# Zeitprotokoll – Installer (Version aus backend/package.json)
# Prüft alle Abhängigkeiten und installiert was fehlt

set -eo pipefail
INSTALL_DIR="$HOME/zeitprotokoll"
LOG="$INSTALL_DIR/install.log"
mkdir -p "$INSTALL_DIR"

log() { echo "[$(date '+%H:%M:%S')] $*" | tee -a "$LOG"; }
ok()  { echo "  ✓ $*"; }
err() { echo "  ✗ $*"; exit 1; }

echo ""
echo "╔══════════════════════════════════════════╗"
ZP_VER=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$(dirname "$0")/backend/package.json" 2>/dev/null | head -1)
printf "║   Zeitprotokoll v%-8s – Installation ║\n" "${ZP_VER:-?}"
echo "╚══════════════════════════════════════════╝"
echo ""

# 0. Bestehende Datenbank sichern (falls vorhanden) – VOR jeder Änderung,
#    unabhängig davon ob es eine Neuinstallation oder ein Update ist.
DB_FILE="$HOME/Documents/Zeitprotokoll/zeitprotokoll.db"
BACKUP_DIR="$HOME/Documents/Zeitprotokoll/backup"
if [ -f "$DB_FILE" ]; then
  mkdir -p "$BACKUP_DIR"
  PREUPDATE_BACKUP="$BACKUP_DIR/zeitprotokoll-preupdate-$(date +%Y%m%d-%H%M%S).db"
  cp "$DB_FILE" "$PREUPDATE_BACKUP"
  ok "Bestehende Datenbank gesichert: $PREUPDATE_BACKUP"
else
  log "Keine bestehende Datenbank gefunden – frische Installation"
fi

# 1. macOS-Check
if [[ "$OSTYPE" != "darwin"* ]]; then
  echo "Hinweis: Kein macOS erkannt – Linux-Server-Modus wird angenommen"
  IS_MAC=false
else
  IS_MAC=true
fi

# 2. Xcode Command Line Tools – für dieses Projekt NICHT zwingend nötig
#    (alle Abhängigkeiten sind reines JS bzw. bringen vorkompilierte Binaries mit,
#    seit der Umstellung von better-sqlite3 auf node:sqlite ist keine native
#    Kompilierung mehr erforderlich). Nur informativ prüfen, nicht blockieren.
if $IS_MAC; then
  if xcode-select -p &>/dev/null; then
    ok "Xcode Command Line Tools: $(xcode-select -p)"
  else
    log "Hinweis: Xcode Command Line Tools nicht gefunden – für Zeitprotokoll nicht erforderlich, wird übersprungen"
  fi
fi

# 3. Node.js Version
log "Prüfe Node.js…"
if ! command -v node &>/dev/null; then
  err "Node.js nicht gefunden. Bitte von https://nodejs.org installieren."
fi
NODE_VER=$(node -e "console.log(process.version.slice(1).split('.')[0])")
if [ "$NODE_VER" -lt 22 ]; then
  err "Node.js ≥ 22 benötigt (aktuell: v$NODE_VER) – Zeitprotokoll nutzt node:sqlite, das erst ab Node 22 verfügbar ist. Bitte aktualisieren: https://nodejs.org (oder 'brew install node@22')"
fi
ok "Node.js $(node --version)"

# 4. npm
log "Prüfe npm…"
ok "npm $(npm --version)"

# 5. Backend Dependencies (node:sqlite ist in Node.js 22+ eingebaut, keine Kompilierung nötig)
log "Installiere Backend-Dependencies…"
cd "$INSTALL_DIR/backend"
npm install --no-fund --no-audit 2>&1 | tail -3
ok "Backend Dependencies installiert"

# 6. Frontend Dependencies + Build
log "Installiere Frontend-Dependencies…"
cd "$INSTALL_DIR/frontend"
npm install --no-fund --no-audit 2>&1 | tail -3
log "Baue Frontend…"
npm run build 2>&1 | tail -3
ok "Frontend gebaut"

# 7. Verzeichnisse anlegen
mkdir -p "$HOME/Documents/Zeitprotokoll/backup"
ok "Datenverzeichnis: ~/Documents/Zeitprotokoll/"

# 8. .env erstellen falls nicht vorhanden
if [ ! -f "$INSTALL_DIR/backend/.env" ]; then
  echo "MODE=standalone" > "$INSTALL_DIR/backend/.env"
  ok ".env erstellt (standalone-Modus)"
fi

# 8b. SSH-Sitzungsprotokoll in ~/.zshrc einhängen (idempotent)
ZSHRC="$HOME/.zshrc"
SSHLOG_LINE='source ~/zeitprotokoll/scripts/ssh-log.zsh  # Zeitprotokoll SSH-Erfassung'
if ! grep -q "zeitprotokoll/scripts/ssh-log.zsh" "$ZSHRC" 2>/dev/null; then
  echo "$SSHLOG_LINE" >> "$ZSHRC"
  echo "✓ SSH-Erfassung in ~/.zshrc eingetragen (gilt ab neuem Terminal-Fenster)"
else
  echo "✓ SSH-Erfassung bereits in ~/.zshrc"
fi

# 9. LaunchAgents (Mac)
if $IS_MAC; then
  LA_DIR="$HOME/Library/LaunchAgents"
  mkdir -p "$LA_DIR"

  # Server LaunchAgent
  cat > "$LA_DIR/com.user.zeitprotokoll.server.plist" << PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.user.zeitprotokoll.server</string>
  <key>ProgramArguments</key>
  <array>
    <string>$(command -v node)</string>
    <string>$INSTALL_DIR/backend/server.js</string>
  </array>
  <key>WorkingDirectory</key><string>$INSTALL_DIR/backend</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>/tmp/zeitprotokoll.log</string>
  <key>StandardErrorPath</key><string>/tmp/zeitprotokoll-err.log</string>
</dict>
</plist>
PLIST

  # Stündliche Erinnerung – entfernt (nicht mehr gewünscht). Falls von einer
  # älteren Installation noch aktiv, hier sauber deaktivieren und aufräumen.
  if [ -f "$LA_DIR/com.user.zeitprotokoll.hourly.plist" ]; then
    launchctl bootout gui/$(id -u) "$LA_DIR/com.user.zeitprotokoll.hourly.plist" 2>/dev/null || true
    rm -f "$LA_DIR/com.user.zeitprotokoll.hourly.plist"
    ok "Alte stündliche Erinnerung deaktiviert und entfernt"
  fi
  # „Guten Morgen – Arbeitstag starten?“ (alte Anmelde-Abfrage, v6.36 abgeschafft): jeden LaunchAgent entfernen, der startup.sh startet
  for f in "$LA_DIR"/*.plist; do
    [ -f "$f" ] || continue
    if grep -q "zeitprotokoll" "$f" 2>/dev/null && grep -q "startup.sh" "$f" 2>/dev/null; then
      launchctl bootout gui/$(id -u) "$f" 2>/dev/null || true
      rm -f "$f"
      ok "Alte Abfrage „Arbeitstag starten?“ beim Anmelden entfernt ($(basename "$f"))"
    fi
  done

  # Backup LaunchAgent (täglich 11:00 Uhr)
  cat > "$LA_DIR/com.user.zeitprotokoll.backup.plist" << PLIST3
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.user.zeitprotokoll.backup</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/curl</string>
    <string>-sf</string>
    <string>-X</string><string>POST</string>
    <string>http://localhost:3747/api/admin/backup</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict><key>Hour</key><integer>11</integer><key>Minute</key><integer>0</integer></dict>
  <key>RunAtLoad</key><false/>
</dict>
</plist>
PLIST3

  ok "LaunchAgents angelegt"

  # LaunchAgents laden
  for plist in server backup; do
    launchctl bootout gui/$(id -u) "$LA_DIR/com.user.zeitprotokoll.$plist.plist" 2>/dev/null || true
    launchctl bootstrap gui/$(id -u) "$LA_DIR/com.user.zeitprotokoll.$plist.plist"
  done
  ok "LaunchAgents gestartet"

  # 10. Health- & Datenkonsistenz-Check nach dem (Neu-)Start
  log "Warte auf Serverstart…"
  sleep 3
  HEALTH=$(curl -sf http://localhost:3747/api/health 2>/dev/null || echo "")
  if [[ "$HEALTH" == *'"ok":true'* ]]; then
    ok "Server erreichbar: $HEALTH"
  else
    echo "  ✗ Server antwortet nicht auf http://localhost:3747/api/health"
    echo "    Bitte prüfen: /tmp/zeitprotokoll-err.log"
  fi

  INTEGRITY=$(curl -sf http://localhost:3747/api/check/integrity 2>/dev/null || echo "")
  if [ -n "$INTEGRITY" ]; then
    ISSUE_COUNT=$(echo "$INTEGRITY" | grep -o '"total":[0-9]*' | head -1 | cut -d: -f2)
    ISSUE_COUNT=${ISSUE_COUNT:-0}
    if [ "$ISSUE_COUNT" = "0" ]; then
      ok "Datenkonsistenz-Check: keine Auffälligkeiten"
    else
      echo "  ℹ Datenkonsistenz-Check: $ISSUE_COUNT Hinweis(e) – Details unter Einstellungen → Datenkonsistenz"
    fi
  fi
fi

echo ""
echo "╔══════════════════════════════════════════╗"
echo "║   Installation abgeschlossen ✓           ║"
echo "║                                          ║"
echo "║   http://localhost:3747                  ║"
echo "╚══════════════════════════════════════════╝"
echo ""
