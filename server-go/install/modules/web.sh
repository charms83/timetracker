# shellcheck shell=bash
# Modul: React-Oberfläche nach /usr/share/zeitprotokoll/web

module_web_meta() {
  MODULE_ID=web
  MODULE_NAME="Oberfläche installieren"
  MODULE_DESC="Gebaute Web-UI (web/) ausliefern"
}

module_web_run() {
  module_web_meta
  zp_log "── $MODULE_NAME ──"
  zp_require_root

  local pkg repo src=""
  pkg="$(zp_package_dir)"
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

  if [ -d "$pkg/web" ] && [ -f "$pkg/web/index.html" ]; then
    src="$pkg/web"
  elif [ -f "$repo/web/index.html" ]; then
    src="$repo/web"
  elif [ -x "$repo/scripts/build-web.sh" ]; then
    zp_log "  Baue Oberfläche (scripts/build-web.sh) …"
    if [ "$ZP_INSTALL_DRY" = 1 ]; then
      zp_log "  [dry-run] $repo/scripts/build-web.sh"
      src="$repo/web"
    else
      "$repo/scripts/build-web.sh"
      src="$repo/web"
    fi
  fi

  if [ -z "$src" ] || { [ "$ZP_INSTALL_DRY" != 1 ] && [ ! -f "$src/index.html" ]; }; then
    zp_warn "Keine Oberfläche – Server läuft ohne ZP_STATIC_DIR (nur API/Admin). Später build-web.sh."
    return 0
  fi

  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] cp -R $src → $ZP_WEB"
  else
    mkdir -p "$(dirname "$ZP_WEB")"
    rm -rf "$ZP_WEB"
    cp -R "$src" "$ZP_WEB"
    chmod -R a+rX "$ZP_WEB"
  fi
  zp_ok "Oberfläche unter $ZP_WEB"
  return 0
}
