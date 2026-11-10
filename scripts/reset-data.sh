#!/bin/bash
# ════════════════════════════════════════════════════════════════
#  Zeitprotokoll – Komplett-Reset
#  Löscht: Zeiteinträge, Arbeitstage, Pausen, Tageslogs, SSH-Sitzungen,
#          Systemlogs, Monatsabschlüsse, Saldo-Korrekturen, Planung,
#          Tickets, Projekte, Kunden.
#  Bleibt erhalten: Einstellungen (Sollstunden, Jira/Tempo-Config,
#          Urlaubstage/Jahr), Urlaub/Krankheit/Feiertage (absences).
#  Vor jeder Löschung wird ein Backup der Datenbank angelegt.
# ════════════════════════════════════════════════════════════════
set -e
DB="$HOME/Documents/Zeitprotokoll/zeitprotokoll.db"
PLIST="$HOME/Library/LaunchAgents/com.user.zeitprotokoll.server.plist"
SSHLOG="$HOME/.zeitprotokoll/ssh_sessions.log"
TS=$(date +%Y%m%d-%H%M%S)
BAKDIR="$HOME/Documents/Zeitprotokoll/backup"
BAK="$BAKDIR/zeitprotokoll-${TS}-vor-reset.db"

[ -f "$DB" ] || { echo "✗ Datenbank nicht gefunden: $DB"; exit 1; }

echo "════════════════════════════════════════════════"
echo " Zeitprotokoll – Komplett-Reset"
echo "════════════════════════════════════════════════"
echo "Das löscht ALLE Zeiten, Projekte, Tickets und Kunden."
echo "Urlaub/Krankheit/Feiertage und Einstellungen bleiben."
echo "Ein Backup wird vorher angelegt unter:"
echo "  $BAK"
echo
read -p "Wirklich fortfahren? Tippe LOESCHEN zum Bestätigen: " CONFIRM
if [ "$CONFIRM" != "LOESCHEN" ]; then
  echo "Abgebrochen – es wurde nichts verändert."
  exit 1
fi

echo "1) Server stoppen…"
launchctl bootout "gui/$(id -u)" "$PLIST" 2>/dev/null || true
sleep 1

echo "2) Backup anlegen…"
mkdir -p "$BAKDIR"
sqlite3 "$DB" "PRAGMA wal_checkpoint(TRUNCATE);" 2>/dev/null || true
cp "$DB" "$BAK"

echo "3) Daten löschen…"
sqlite3 "$DB" <<'SQL'
PRAGMA foreign_keys=OFF;
DELETE FROM time_entries;
DELETE FROM work_days;
DELETE FROM pauses;
DELETE FROM daily_logs;
DELETE FROM ssh_sessions;
DELETE FROM system_logs;
DELETE FROM month_closings;
DELETE FROM balance_corrections;
DELETE FROM plan_tasks;
DELETE FROM tasks;
DELETE FROM projects;
DELETE FROM customers;
UPDATE timer_state SET project_id=NULL, task_id=NULL, plan_task_id=NULL, comment=NULL, start_time=NULL WHERE id=1;
UPDATE pause_state SET start_time=NULL WHERE id=1;
DELETE FROM sqlite_sequence WHERE name IN
  ('time_entries','work_days','daily_logs','pauses','ssh_sessions','system_logs',
   'month_closings','balance_corrections','plan_tasks','tasks','projects','customers');
UPDATE settings SET value=date('now','localtime') WHERE key='tracking_start_date';
DELETE FROM settings WHERE key='ssh_log_offset';
VACUUM;
SQL

echo "4) SSH-Protokolldatei archivieren…"
if [ -f "$SSHLOG" ]; then
  mv "$SSHLOG" "${SSHLOG%.log}-archiv-${TS}.log"
  echo "   alte Datei gesichert als ${SSHLOG%.log}-archiv-${TS}.log"
fi
mkdir -p "$(dirname "$SSHLOG")"
: > "$SSHLOG"

echo "5) Server neu starten…"
launchctl bootstrap "gui/$(id -u)" "$PLIST" 2>/dev/null || true
launchctl kickstart -k "gui/$(id -u)/com.user.zeitprotokoll.server" 2>/dev/null || true
sleep 3
if curl -sf http://127.0.0.1:3747/api/health >/dev/null; then
  echo "   ✓ Server läuft wieder"
else
  echo "   ⚠ Server antwortet nicht – ggf. Mac neu starten oder manuell prüfen"
fi

echo
echo "✓ Fertig. Alles Erfasste ist gelöscht, du startest bei null."
echo "  Backup der alten Daten liegt hier (zur Sicherheit, wird nicht automatisch gelöscht):"
echo "  $BAK"
