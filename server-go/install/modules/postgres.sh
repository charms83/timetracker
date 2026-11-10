# shellcheck shell=bash
# Modul: PostgreSQL-Datenbank und Rolle anlegen (lokal)

module_postgres_meta() {
  MODULE_ID=postgres
  MODULE_NAME="PostgreSQL einrichten"
  MODULE_DESC="Datenbank und Rolle anlegen (lokal/peer oder Passwort)"
}

module_postgres_run() {
  module_postgres_meta
  zp_log "── $MODULE_NAME ──"
  if ! command -v psql >/dev/null 2>&1; then
    if [ "$ZP_INSTALL_DRY" = 1 ]; then
      zp_warn "psql fehlt – Dry-run überspringt PostgreSQL"
      return 0
    fi
    zp_die "Befehl fehlt: psql (PostgreSQL-Client installieren oder Modul überspringen)"
  fi

  local db="$ZP_DB_NAME" role="$ZP_DB_USER" pass="" host="localhost" port="5432" ssl="disable"
  if [ "$ZP_INSTALL_YES" != 1 ]; then
    zp_ask db "Datenbankname" "$db"
    zp_ask role "Datenbankrolle" "$role"
    zp_ask host "Host" "$host"
    zp_ask port "Port" "$port"
    zp_ask_secret pass "Passwort für $role (leer = peer/trust lokal)"
    zp_ask ssl "sslmode" "$ssl"
  else
    pass="${ZP_DB_PASSWORD:-}"
  fi

  local admin_db="${ZP_PG_ADMIN_DB:-postgres}"
  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] CREATE ROLE/DB $role / $db @ $host:$port"
    if [ -n "$pass" ]; then
      ZP_DATABASE_URL="postgres://${role}:***@${host}:${port}/${db}?sslmode=${ssl}"
    else
      ZP_DATABASE_URL="postgres://${role}@${host}:${port}/${db}?sslmode=${ssl}"
    fi
    export ZP_DATABASE_URL
    return 0
  fi

  if psql -h "$host" -p "$port" -d "$admin_db" -tAc "SELECT 1 FROM pg_roles WHERE rolname='$role'" 2>/dev/null | grep -q 1; then
    zp_ok "Rolle $role existiert bereits"
  else
    if [ -n "$pass" ]; then
      # SQL-Datei mit escaped Passwort (kein Klartext in ps -f)
      local tmp
      tmp="$(mktemp)"
      printf "CREATE ROLE \"%s\" LOGIN PASSWORD '%s';\n" "$role" "$(printf '%s' "$pass" | sed "s/'/''/g")" > "$tmp"
      chmod 600 "$tmp"
      zp_run psql -h "$host" -p "$port" -d "$admin_db" -v ON_ERROR_STOP=1 -f "$tmp"
      rm -f "$tmp"
    else
      zp_run psql -h "$host" -p "$port" -d "$admin_db" -v ON_ERROR_STOP=1 \
        -c "CREATE ROLE \"$role\" LOGIN;"
    fi
    zp_ok "Rolle $role angelegt"
  fi

  if psql -h "$host" -p "$port" -d "$admin_db" -tAc "SELECT 1 FROM pg_database WHERE datname='$db'" 2>/dev/null | grep -q 1; then
    zp_ok "Datenbank $db existiert bereits"
  else
    zp_run psql -h "$host" -p "$port" -d "$admin_db" -v ON_ERROR_STOP=1 \
      -c "CREATE DATABASE \"$db\" OWNER \"$role\";"
    zp_ok "Datenbank $db angelegt"
  fi

  if [ -n "$pass" ]; then
    # URL-Escape für Passwort (minimal: @ : / #)
    local enc
    enc="$(printf '%s' "$pass" | sed -e 's/%/%25/g' -e 's/@/%40/g' -e 's/:/%3A/g' -e 's/\//%2F/g' -e 's/#/%23/g')"
    ZP_DATABASE_URL="postgres://${role}:${enc}@${host}:${port}/${db}?sslmode=${ssl}"
  else
    ZP_DATABASE_URL="postgres://${role}@${host}:${port}/${db}?sslmode=${ssl}"
  fi
  export ZP_DATABASE_URL
  zp_ok "DATABASE_URL vorbereitet (wird im Config-Modul geschrieben)"
  return 0
}
