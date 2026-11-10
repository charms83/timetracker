# shellcheck shell=bash
# Auswahldialoge (Terminal-Menüs) – ohne dialog/whiptail-Abhängigkeit.

zp_banner() {
  cat <<EOF

╔══════════════════════════════════════════════════╗
║   Zeitprotokoll-Server – modularer Installer     ║
╚══════════════════════════════════════════════════╝

EOF
}

# zp_menu <title> → setzt ZP_MENU_CHOICE auf den gewählten Schlüssel
# Aufruf: zp_menu "Titel" "key1:Label 1" "key2:Label 2" ...
zp_menu() {
  local title="$1"; shift
  local -a keys=() labels=()
  local i=1 item key label
  echo ""
  echo "── $title ──"
  for item in "$@"; do
    key="${item%%:*}"
    label="${item#*:}"
    keys+=("$key")
    labels+=("$label")
    printf '  %d) %s\n' "$i" "$label"
    i=$((i + 1))
  done
  printf '  %d) Abbrechen\n' "$i"
  local choice
  while true; do
    printf 'Auswahl: '
    read -r choice || { ZP_MENU_CHOICE=""; return 1; }
    if [ "$choice" = "$i" ] || [ "$choice" = "q" ] || [ "$choice" = "Q" ]; then
      ZP_MENU_CHOICE=""
      return 1
    fi
    if [[ "$choice" =~ ^[0-9]+$ ]] && [ "$choice" -ge 1 ] && [ "$choice" -lt "$i" ]; then
      ZP_MENU_CHOICE="${keys[$((choice - 1))]}"
      return 0
    fi
    echo "Ungültig – Zahl 1–$i."
  done
}

zp_ask() {
  # zp_ask VAR "Frage" ["Standard"]
  local var="$1" q="$2" def="${3:-}" ans
  if [ -n "$def" ]; then
    printf '%s [%s]: ' "$q" "$def"
  else
    printf '%s: ' "$q"
  fi
  read -r ans || ans=""
  if [ -z "$ans" ]; then ans="$def"; fi
  printf -v "$var" '%s' "$ans"
}

zp_ask_secret() {
  local var="$1" q="$2" ans
  printf '%s: ' "$q"
  stty -echo 2>/dev/null || true
  read -r ans || ans=""
  stty echo 2>/dev/null || true
  echo ""
  printf -v "$var" '%s' "$ans"
}

zp_multi_select() {
  # Setzt ZP_SELECTED (Leerzeichen-getrennt) aus Liste key:Label
  # Mit Standard-Vorauswahl in ZP_PRESELECT (Leerzeichen-getrennt)
  local title="$1"; shift
  local -a keys=() labels=() on=()
  local i=0 item key label
  echo ""
  echo "── $title ──"
  echo "  (Nummer tippen zum Umschalten, Enter = fertig, a = alle, n = keine)"
  for item in "$@"; do
    key="${item%%:*}"
    label="${item#*:}"
    keys+=("$key")
    labels+=("$label")
    if [[ " ${ZP_PRESELECT:-} " == *" $key "* ]]; then on+=(1); else on+=(0); fi
  done
  while true; do
    echo ""
    for i in "${!keys[@]}"; do
      if [ "${on[$i]}" = 1 ]; then printf '  [%d] ● %s\n' "$((i + 1))" "${labels[$i]}"
      else printf '  [%d] ○ %s\n' "$((i + 1))" "${labels[$i]}"; fi
    done
    printf 'Umschalten / Enter: '
    local c
    read -r c || c=""
    if [ -z "$c" ]; then break; fi
    if [ "$c" = a ] || [ "$c" = A ]; then
      for i in "${!on[@]}"; do on[$i]=1; done
      continue
    fi
    if [ "$c" = n ] || [ "$c" = N ]; then
      for i in "${!on[@]}"; do on[$i]=0; done
      continue
    fi
    if [[ "$c" =~ ^[0-9]+$ ]] && [ "$c" -ge 1 ] && [ "$c" -le "${#keys[@]}" ]; then
      i=$((c - 1))
      if [ "${on[$i]}" = 1 ]; then on[$i]=0; else on[$i]=1; fi
    fi
  done
  ZP_SELECTED=""
  for i in "${!keys[@]}"; do
    [ "${on[$i]}" = 1 ] && ZP_SELECTED+="${keys[$i]} "
  done
  ZP_SELECTED="${ZP_SELECTED% }"
}
