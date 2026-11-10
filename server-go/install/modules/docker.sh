# shellcheck shell=bash
# Modul: Docker Compose (App + PostgreSQL) – ohne Ansible

module_docker_meta() {
  MODULE_ID=docker
  MODULE_NAME="Docker Compose"
  MODULE_DESC="Server + PostgreSQL in Containern (einfach, lokal/VM)"
}

module_docker_run() {
  module_docker_meta
  zp_log "── $MODULE_NAME ──"

  local repo dir
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  dir="$repo/deploy/docker"
  [ -f "$dir/docker-compose.yml" ] || zp_die "deploy/docker fehlt"

  if ! command -v docker >/dev/null 2>&1; then
    if [ "$ZP_INSTALL_DRY" = 1 ]; then
      zp_warn "docker fehlt – Dry-run zeigt nur die Schritte"
    else
      zp_die "docker fehlt – Binary-/systemd-Profil nutzen oder Docker installieren"
    fi
  elif ! docker compose version >/dev/null 2>&1; then
    if [ "$ZP_INSTALL_DRY" = 1 ]; then
      zp_warn "docker compose fehlt – Dry-run ohne Start"
    else
      zp_die "docker compose fehlt (Plugin v2)"
    fi
  else
    zp_ok "docker + compose vorhanden"
  fi

  local envf="$dir/.env"
  if [ ! -f "$envf" ]; then
    if [ "$ZP_INSTALL_DRY" = 1 ]; then
      zp_log "  [dry-run] cp .env.example → .env"
    else
      cp "$dir/.env.example" "$envf"
      local key pass
      key="$(zp_gen_key)"
      pass="$(zp_gen_key | head -c 24)"
      # portable sed -i
      sed -e "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=$pass|" \
          -e "s|^ZP_SECRET_KEY=.*|ZP_SECRET_KEY=$key|" \
          "$dir/.env.example" > "$envf"
      chmod 0600 "$envf"
      zp_ok ".env angelegt ($envf) – Passwort und Schlüssel erzeugt"
      zp_warn "Bitte ZP_PUBLIC_URL prüfen und .env sichern (Tresor)."
    fi
  else
    zp_ok "Bestehende .env behalten"
    if grep -q 'CHANGEME' "$envf" 2>/dev/null; then
      zp_die "Noch CHANGEME in $envf – bitte ausfüllen"
    fi
  fi

  if [ "$ZP_INSTALL_YES" != 1 ]; then
    zp_confirm "docker compose up -d --build jetzt starten?" || {
      zp_log "  Manuell: cd $dir && docker compose up -d --build"
      return 0
    }
  fi

  if [ "$ZP_INSTALL_DRY" = 1 ] || ! command -v docker >/dev/null 2>&1; then
    zp_log "  [dry-run] (cd $dir && docker compose up -d --build)"
    return 0
  fi

  ( cd "$dir" && docker compose up -d --build )
  zp_ok "Container gestartet"
  zp_log "  Prüfen: curl -fsS http://127.0.0.1:8080/healthz"
  zp_log "  Token:  docker compose -f $dir/docker-compose.yml logs app | grep -i token"
  zp_log "  Dann:   http://127.0.0.1:8080/setup"
  return 0
}
