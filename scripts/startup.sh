#!/bin/bash
# Beim Mac-Login: fragt ob Arbeitstag gestartet werden soll
API="http://localhost:3747/api"
STATE="$HOME/.zeitprotokoll"
mkdir -p "$STATE"
log() { echo "$(date '+%H:%M:%S') $*" >> "$STATE/startup.log"; }

# Warten bis Server ready
sleep 4
for i in {1..15}; do
  curl -sf "$API/timer/status" >/dev/null 2>&1 && break
  sleep 2
done

# v6.36: Abfrage ist standardmäßig aus (Einstellungen › Arbeitszeit › „Beim Anmelden fragen …“)
P=$(curl -sf "$API/settings" 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('workday_prompt','0'))" 2>/dev/null)
if [ "$P" != "1" ]; then log "Abfrage ausgeschaltet"; exit 0; fi

# Arbeitstag schon aktiv?
WD=$(curl -sf "$API/workday" 2>/dev/null)
ACTIVE=$(echo "$WD" | python3 -c \
  "import sys,json; d=json.load(sys.stdin); w=d.get('workday'); print('yes' if w and not w.get('end_time') else 'no')" 2>/dev/null)

if [ "$ACTIVE" = "yes" ]; then log "Arbeitstag bereits aktiv"; exit 0; fi

ANTWORT=$(osascript <<'EOF'
try
  tell application "System Events"
    set d to display dialog "Guten Morgen! ☀️\n\nArbeitstag starten?" ¬
      with title "Zeitprotokoll" ¬
      buttons {"Nein", "Ja, starten"} ¬
      default button "Ja, starten" giving up after 60
    if gave up of d then return "no"
    return button returned of d
  end tell
on error
  return "no"
end try
EOF
)

if [ "$ANTWORT" = "Ja, starten" ]; then
  curl -sf -X POST "$API/workday/start" -H "Content-Type: application/json" >/dev/null
  log "Arbeitstag gestartet"
  sleep 1
  open "http://localhost:3747"
else
  log "Nicht gestartet"
fi
