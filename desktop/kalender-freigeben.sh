#!/bin/bash
# Ermöglicht der Zeitprotokoll-App, nach Kalenderzugriff zu fragen.
# (macOS zeigt den Dialog nur, wenn die App erklärt, wofür sie den Kalender braucht.)
set -e
APP="/Applications/Zeitprotokoll.app"
PL="$APP/Contents/Info.plist"
PB=/usr/libexec/PlistBuddy
TXT="Zeitprotokoll liest deine Termine, um Zeitbuchungen vorzuschlagen. Es wird nichts verändert und nichts weitergegeben."
[ -f "$PL" ] || { echo "✗ $APP nicht gefunden"; exit 1; }
echo "→ Zeitprotokoll beenden"; osascript -e 'quit app "Zeitprotokoll"' 2>/dev/null || true; sleep 2
for k in NSCalendarsUsageDescription NSCalendarsFullAccessUsageDescription; do
  $PB -c "Delete :$k" "$PL" 2>/dev/null || true
  $PB -c "Add :$k string $TXT" "$PL"
done
$PB -c "Delete :NSAppleEventsUsageDescription" "$PL" 2>/dev/null || true
$PB -c "Add :NSAppleEventsUsageDescription string Zeitprotokoll prüft, ob im Browser ein Meeting-Tab offen ist (nur Adresse, lokal)." "$PL"
# auch für künftige Neubauten merken
EX="$HOME/zeitprotokoll/desktop/extra.plist"
if [ -f "$EX" ] && ! grep -q NSCalendarsFullAccessUsageDescription "$EX"; then
  for k in NSCalendarsUsageDescription NSCalendarsFullAccessUsageDescription; do $PB -c "Add :$k string $TXT" "$EX"; done
fi
xattr -cr "$APP"
codesign --force --deep --sign - "$APP" 2>/dev/null && echo "  ✓ lokal neu signiert"
tccutil reset Calendar com.example.zeitprotokoll >/dev/null 2>&1 || true
echo "→ Zeitprotokoll starten – gleich fragt macOS nach dem Kalender: „Vollzugriff erlauben“ bzw. „OK“"
open "$APP"
echo "✓ Fertig. Kontrolle: Zeitprotokoll › Einstellungen › Erkennung › Status „Kalender“ (nach ca. 30 Sekunden)."
