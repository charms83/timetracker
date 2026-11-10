# shellcheck shell=bash
# Modul: Voraussetzungen prüfen (ohne Root nötig)

module_check_meta() {
  MODULE_ID=check
  MODULE_NAME="Voraussetzungen prüfen"
  MODULE_DESC="OS, Architektur, systemd/psql/oc – nur lesen"
}

module_check_run() {
  module_check_meta
  zp_log "── $MODULE_NAME ──"
  zp_ok "OS: $(uname -s) $(uname -r)"
  zp_ok "Architektur: $(uname -m) → $(zp_arch)"
  if command -v systemctl >/dev/null 2>&1; then zp_ok "systemd vorhanden"
  else zp_warn "systemd fehlt – Dienst-Modul später überspringen oder OpenShift nutzen"; fi
  if command -v psql >/dev/null 2>&1; then zp_ok "psql: $(psql --version | head -1)"
  else zp_warn "psql fehlt – PostgreSQL-Modul braucht Client-Werkzeuge"; fi
  if command -v pg_isready >/dev/null 2>&1; then
    if pg_isready -q 2>/dev/null; then zp_ok "PostgreSQL erreichbar (pg_isready)"
    else zp_warn "PostgreSQL antwortet nicht (pg_isready)"; fi
  fi
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    zp_ok "Docker Compose vorhanden"
  elif command -v docker >/dev/null 2>&1; then
    zp_warn "docker ohne compose-Plugin – Docker-Profil braucht „docker compose“"
  else
    zp_warn "docker fehlt – Docker-Profil nicht nutzbar (Binary/OpenShift ok)"
  fi
  if command -v oc >/dev/null 2>&1; then zp_ok "OpenShift-CLI (oc) vorhanden"
  elif command -v kubectl >/dev/null 2>&1; then zp_ok "kubectl vorhanden"
  else zp_warn "oc/kubectl fehlen – OpenShift-Modul nur als Anleitung"; fi
  local pkg
  pkg="$(zp_package_dir)"
  zp_log "  Paket-/Quellenverzeichnis: $pkg"
  local a bin
  a="$(zp_arch)"
  bin="$pkg/zeitprotokoll-server-linux-$a"
  if [ -x "$bin" ]; then zp_ok "Binary im Paket: $bin"
  elif [ -x "$pkg/../bin/server" ]; then zp_ok "Dev-Binary: $pkg/../bin/server"
  elif command -v go >/dev/null 2>&1; then zp_warn "Kein fertiges Binary – Binary-Modul kann mit Go bauen"
  else zp_warn "Kein Binary und kein Go – zuerst scripts/package.sh oder Go installieren"; fi
  if [ -d "$pkg/web" ] || [ -f "$pkg/../web/index.html" ]; then zp_ok "Weboberfläche gefunden"
  else zp_warn "Keine web/ – Oberfläche später mit scripts/build-web.sh bauen"; fi
  return 0
}
