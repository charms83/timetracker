# ════════════════════════════════════════════════════════════════
#  Zeitprotokoll – SSH-Sitzungsprotokoll (v1.0)
#  Wird aus ~/.zshrc geladen:  source ~/zeitprotokoll/scripts/ssh-log.zsh
#  Schreibt pro Sitzung eine Start- und eine Ende-Zeile nach
#  ~/.zeitprotokoll/ssh_sessions.log – das Backend importiert sie.
#  Format (TAB-getrennt):
#    S <id> <epoch> <ziel> <hostname> <user> <port> <remote-befehl>
#    E <id> <epoch> <exitcode>
# ════════════════════════════════════════════════════════════════

# Zeitstempel im Shell-Verlauf (macht spätere Rekonstruktion möglich)
setopt EXTENDED_HISTORY INC_APPEND_HISTORY

typeset -g ZP_SSH_LOG="${ZP_SSH_LOG:-$HOME/.zeitprotokoll/ssh_sessions.log}"

_zp_ssh_clean() { local s="$*"; s=${s//$'\t'/ }; s=${s//$'\n'/ }; print -rn -- "$s"; }

# Ein-/Ausschalter (auch von der App unter "SSH" umschaltbar):
#   sshtrack-off   – Zeiterfassung für SSH-Sitzungen ausschalten
#   sshtrack-on    – wieder einschalten
#   sshtrack-status – aktuellen Zustand anzeigen
typeset -g ZP_SSH_FLAG="${ZP_SSH_FLAG:-$HOME/.zeitprotokoll/ssh_tracking_disabled}"
sshtrack-off()     { mkdir -p "${ZP_SSH_FLAG:h}" 2>/dev/null; date "+deaktiviert %s" > "$ZP_SSH_FLAG"; echo "SSH-Zeiterfassung deaktiviert." }
sshtrack-on()      { rm -f "$ZP_SSH_FLAG"; echo "SSH-Zeiterfassung aktiviert." }
sshtrack-status()  { [[ -f "$ZP_SSH_FLAG" ]] && echo "SSH-Zeiterfassung: AUS" || echo "SSH-Zeiterfassung: AN" }

ssh() {
  emulate -L zsh
  setopt localtraps

  # Schalter aus? Dann ganz normal verbinden, nichts mitschreiben, nichts prüfen.
  [[ -f "$ZP_SSH_FLAG" ]] && { /usr/bin/ssh "$@"; return $?; }

  # Ziel (erstes Nicht-Options-Argument) und evtl. Remote-Befehl ermitteln
  local -a args; args=("$@")
  local optval="BbcDEeFIiJLlmOoPpQRSWw"
  local dest="" i=1 n=${#args} j c
  while (( i <= n )); do
    local x="${args[i]}"
    if [[ $x == -- ]]; then (( i++ )); dest="${args[i]}"; break
    elif [[ $x == -?* ]]; then
      for (( j=2; j<=${#x}; j++ )); do
        c="${x[j]}"
        if [[ $optval == *$c* ]]; then
          (( j == ${#x} )) && (( i++ ))   # Wert steht im nächsten Argument
          break
        fi
      done
    else dest="$x"; break
    fi
    (( i++ ))
  done

  # Kein Ziel (z.B. "ssh -V") → nicht protokollieren
  if [[ -z $dest ]]; then /usr/bin/ssh "$@"; return $?; fi

  local remote=""
  (( i < n )) && remote="$(_zp_ssh_clean "${(@)args[i+1,n]}")"

  # Hostname/User/Port so auflösen, wie ssh es mit ~/.ssh/config tut.
  # WICHTIG: hier bewusst /usr/bin/ssh direkt statt "command ssh" – falls im PATH
  # vor dem echten ssh-Client ein eigenes Alias-Skript liegt (z.B. ~/bin/ssh),
  # kennt das meist keine Flags wie -G und würde hier hängen bleiben/fehlschlagen.
  local real_ssh="/usr/bin/ssh"; [[ -x $real_ssh ]] || real_ssh="ssh"
  local host="" user="" port="" k v
  while read -r k v; do
    case $k in
      hostname) host=$v ;;
      user)     user=$v ;;
      port)     port=$v ;;
    esac
  done < <("$real_ssh" -G "$@" 2>/dev/null)

  local start=$EPOCHSECONDS
  [[ -z $start ]] && start=$(date +%s)
  local id="${HOST%%.*}-$$-$start-$RANDOM"
  mkdir -p "${ZP_SSH_LOG:h}" 2>/dev/null
  print -r -- "S	$id	$start	$(_zp_ssh_clean $dest)	$host	$user	$port	$remote" >> "$ZP_SSH_LOG"

  # Auch beim Schließen des Terminal-Fensters das Ende festhalten
  local _zp_done=0
  trap 'if (( ! _zp_done )); then _zp_done=1; print -r -- "E	$id	$(date +%s)	129" >> "$ZP_SSH_LOG"; fi' HUP TERM

  /usr/bin/ssh "$@"
  local rc=$?
  if (( ! _zp_done )); then
    _zp_done=1
    print -r -- "E	$id	$(date +%s)	$rc" >> "$ZP_SSH_LOG"
  fi
  return $rc
}

zmodload zsh/datetime 2>/dev/null
