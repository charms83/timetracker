# shellcheck shell=bash
# Modul: systemd-Dienst installieren und starten

module_systemd_meta() {
  MODULE_ID=systemd
  MODULE_NAME="systemd-Dienst"
  MODULE_DESC="Unit installieren, aktivieren, starten"
}

module_systemd_run() {
  module_systemd_meta
  zp_log "── $MODULE_NAME ──"
  zp_require_root

  if [ -z "$ZP_INSTALL_ROOT" ] && ! command -v systemctl >/dev/null 2>&1; then
    zp_warn "Kein systemd – Modul übersprungen"
    return 0
  fi

  local pkg unit_src repo
  pkg="$(zp_package_dir)"
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  if [ -f "$pkg/zeitprotokoll.service" ]; then
    unit_src="$pkg/zeitprotokoll.service"
  else
    unit_src="$repo/deploy/linux/zeitprotokoll.service"
  fi
  [ -f "$unit_src" ] || zp_die "zeitprotokoll.service fehlt"

  if [ ! -f "$ZP_ENV" ] && [ "$ZP_INSTALL_DRY" != 1 ]; then
    zp_die "Konfiguration fehlt ($ZP_ENV) – zuerst Config-Modul"
  fi

  if [ -f "$ZP_ENV" ] && grep -q 'CHANGEME' "$ZP_ENV" 2>/dev/null; then
    zp_die "Noch CHANGEME in $ZP_ENV – bitte Config-Modul ausfüllen"
  fi

  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] install unit → $ZP_UNIT"
    zp_log "  [dry-run] systemctl enable --now zeitprotokoll"
    return 0
  fi

  mkdir -p "$ZP_UNIT_DIR"
  # Bei Testroot Pfade in der Unit anpassen
  if [ -n "$ZP_INSTALL_ROOT" ]; then
    sed -e "s|/usr/local/bin/zeitprotokoll-server|$ZP_BIN|g" \
        -e "s|/etc/zeitprotokoll/zeitprotokoll.env|$ZP_ENV|g" \
        "$unit_src" > "$ZP_UNIT"
  else
    install -m 0644 "$unit_src" "$ZP_UNIT"
    systemctl stop zeitprotokoll 2>/dev/null || true
    systemctl daemon-reload
    systemctl enable zeitprotokoll >/dev/null 2>&1 || true
    systemctl start zeitprotokoll
    zp_ok "Dienst gestartet (systemctl status zeitprotokoll)"
    return 0
  fi
  chmod 0644 "$ZP_UNIT"
  zp_ok "Unit geschrieben nach $ZP_UNIT (Testroot – kein systemctl)"
  return 0
}
