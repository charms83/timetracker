# shellcheck shell=bash
# Modul: Konfiguration /etc/zeitprotokoll/zeitprotokoll.env (Auswahldialog)

module_config_meta() {
  MODULE_ID=config
  MODULE_NAME="Konfiguration"
  MODULE_DESC="Umgebung schreiben (DATABASE_URL, Schlüssel, öffentliche URL)"
}

module_config_run() {
  module_config_meta
  zp_log "── $MODULE_NAME ──"
  zp_require_root

  local pkg example
  pkg="$(zp_package_dir)"
  example=""
  if [ -f "$pkg/zeitprotokoll.env.example" ]; then
    example="$pkg/zeitprotokoll.env.example"
  else
    local repo
    repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
    example="$repo/deploy/linux/zeitprotokoll.env.example"
  fi
  [ -f "$example" ] || zp_die "Vorlage fehlt: zeitprotokoll.env.example"

  if [ -f "$ZP_ENV" ] && [ "$ZP_INSTALL_YES" != 1 ]; then
    if ! zp_confirm "Konfiguration $ZP_ENV existiert – überschreiben?"; then
      zp_ok "Bestehende Konfiguration behalten"
      return 0
    fi
  fi

  local dburl public url_addr key trust
  dburl="${ZP_DATABASE_URL:-}"
  public="${ZP_PUBLIC_URL:-https://zeit.example.com}"
  url_addr="${ZP_ADDR:-127.0.0.1:8080}"
  trust="${ZP_TRUST_PROXY:-1}"
  key=""

  if [ "$ZP_INSTALL_YES" != 1 ]; then
    zp_ask dburl "DATABASE_URL" "${dburl:-postgres://zeitprotokoll@localhost:5432/zeitprotokoll?sslmode=disable}"
    zp_ask public "Öffentliche URL (https …)" "$public"
    zp_ask url_addr "Listen-Adresse ZP_ADDR" "$url_addr"
    zp_ask trust "Proxy davor? ZP_TRUST_PROXY (0/1)" "$trust"
    if [ -f "$ZP_ENV" ] && grep -q '^ZP_SECRET_KEY=.' "$ZP_ENV" 2>/dev/null; then
      zp_log "  Bestehenden ZP_SECRET_KEY behalten (Tokens bleiben lesbar)."
      key="$(grep '^ZP_SECRET_KEY=' "$ZP_ENV" | head -1 | cut -d= -f2-)"
    else
      key="$(zp_gen_key)"
      zp_ok "Neuer ZP_SECRET_KEY erzeugt – bitte im Tresor sichern."
    fi
  else
    dburl="${dburl:-postgres://zeitprotokoll@localhost:5432/zeitprotokoll?sslmode=disable}"
    if [ -f "$ZP_ENV" ] && grep -q '^ZP_SECRET_KEY=.' "$ZP_ENV" 2>/dev/null; then
      key="$(grep '^ZP_SECRET_KEY=' "$ZP_ENV" | head -1 | cut -d= -f2-)"
    else
      key="${ZP_SECRET_KEY:-$(zp_gen_key)}"
    fi
  fi

  [ -n "$dburl" ] || zp_die "DATABASE_URL fehlt"
  [ -n "$key" ] && [ "${#key}" -ge 32 ] || zp_die "ZP_SECRET_KEY mindestens 32 Zeichen"

  local static="$ZP_WEB"
  [ -d "$static" ] || static=""

  local body
  body="$(cat <<EOF
# Erzeugt vom modularen Installer ($(date -u +%Y-%m-%dT%H:%MZ))
DATABASE_URL=$dburl
ZP_SECRET_KEY=$key
ZP_PUBLIC_URL=$public
ZP_ADDR=$url_addr
ZP_TZ=Europe/Berlin
ZP_TRUST_PROXY=$trust
ZP_TRUSTED_PROXIES=1
ZP_PRODUCTION=1
EOF
)"
  if [ -n "$static" ]; then
    body+=$'\n'"ZP_STATIC_DIR=$static"
  fi

  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] schreibe $ZP_ENV"
    printf '%s\n' "$body" | sed 's/^ZP_SECRET_KEY=.*/ZP_SECRET_KEY=***/' | sed 's/^/  | /'
    return 0
  fi

  mkdir -p "$ZP_CONF_DIR"
  chmod 0750 "$ZP_CONF_DIR"
  printf '%s\n' "$body" > "$ZP_ENV"
  chmod 0640 "$ZP_ENV"
  if [ -z "$ZP_INSTALL_ROOT" ] && id "$ZP_USER" >/dev/null 2>&1; then
    chown "root:$ZP_USER" "$ZP_CONF_DIR" "$ZP_ENV" 2>/dev/null || chown "root:root" "$ZP_ENV"
  fi
  zp_ok "Konfiguration: $ZP_ENV"
  zp_warn "Schlüssel und DATABASE_URL nie ins Repository / Chat kopieren."
  return 0
}
