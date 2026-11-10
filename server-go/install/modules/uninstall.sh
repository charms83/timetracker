# shellcheck shell=bash
# Modul: Deinstallation (Dienst + Binary; optional Konfiguration)

module_uninstall_meta() {
  MODULE_ID=uninstall
  MODULE_NAME="Deinstallieren"
  MODULE_DESC="Dienst und Programm entfernen (DB bleibt)"
}

module_uninstall_run() {
  module_uninstall_meta
  zp_log "── $MODULE_NAME ──"
  zp_require_root

  local purge=0
  if [ "${ZP_INSTALL_PURGE:-0}" = 1 ]; then purge=1; fi
  if [ "$ZP_INSTALL_YES" != 1 ]; then
    zp_confirm "Zeitprotokoll-Server deinstallieren?" || { zp_log "Abgebrochen."; return 0; }
    zp_confirm "Auch Konfiguration unter $ZP_CONF_DIR löschen? (Datenbank bleibt!)" && purge=1
  fi

  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] systemctl disable --now zeitprotokoll"
    zp_log "  [dry-run] rm Binary/Web/Unit"
    [ "$purge" = 1 ] && zp_log "  [dry-run] rm -rf $ZP_CONF_DIR"
    return 0
  fi

  if [ -z "$ZP_INSTALL_ROOT" ] && command -v systemctl >/dev/null 2>&1; then
    systemctl disable --now zeitprotokoll 2>/dev/null || true
    systemctl daemon-reload 2>/dev/null || true
  fi
  rm -f "$ZP_BIN" "$ZP_UNIT"
  rm -rf "${ZP_INSTALL_ROOT}/usr/share/zeitprotokoll"
  if [ "$purge" = 1 ]; then
    rm -rf "$ZP_CONF_DIR"
    zp_ok "Konfiguration gelöscht"
  else
    zp_ok "Konfiguration behalten: $ZP_CONF_DIR"
  fi
  zp_ok "Entfernt. Die PostgreSQL-Datenbank wurde nicht angefasst."
  return 0
}
