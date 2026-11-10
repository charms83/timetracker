# shellcheck shell=bash
# Modul: Installation prüfen (healthz / server check)

module_verify_meta() {
  MODULE_ID=verify
  MODULE_NAME="Prüfen"
  MODULE_DESC="healthz und server check"
}

module_verify_run() {
  module_verify_meta
  zp_log "── $MODULE_NAME ──"

  local addr="127.0.0.1:8080"
  if [ -f "$ZP_ENV" ]; then
    # shellcheck disable=SC1090
    set -a
    # Nur sichere Keys laden – Datei ist Key=Value
    # shellcheck source=/dev/null
    . "$ZP_ENV" 2>/dev/null || true
    set +a
    addr="${ZP_ADDR:-$addr}"
    addr="${addr#:}"
    case "$addr" in
      :*) addr="127.0.0.1${addr}" ;;
      *:*) ;;
      *) addr="127.0.0.1:$addr" ;;
    esac
  fi

  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] curl http://$addr/healthz"
    zp_log "  [dry-run] zeitprotokoll-server check"
    return 0
  fi

  local ok=0
  if command -v curl >/dev/null 2>&1; then
    if curl -fsS --max-time 5 "http://${addr}/healthz" >/dev/null 2>&1; then
      zp_ok "healthz antwortet ($addr)"
      ok=1
    else
      zp_warn "healthz noch nicht erreichbar ($addr) – Dienst starten / Log prüfen"
    fi
  else
    zp_warn "curl fehlt – healthz übersprungen"
  fi

  if [ -x "$ZP_BIN" ] && [ -f "$ZP_ENV" ]; then
    if ( set -a; # shellcheck disable=SC1090
         . "$ZP_ENV"; set +a;
         "$ZP_BIN" check ) >/tmp/zp-check.out 2>&1; then
      zp_ok "server check ok"
      ok=1
    else
      zp_warn "server check meldet Probleme (siehe /tmp/zp-check.out)"
      [ "$ZP_INSTALL_QUIET" = 1 ] || sed 's/^/  | /' /tmp/zp-check.out | tail -20
    fi
  fi

  if [ -z "$ZP_INSTALL_ROOT" ] && command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet zeitprotokoll 2>/dev/null; then
      zp_ok "systemd: active"
    else
      zp_warn "systemd: nicht active"
    fi
  fi

  zp_log "  Nächster Schritt: Setup-Token im Log (journalctl -u zeitprotokoll), dann /setup"
  return 0
}
