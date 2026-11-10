# shellcheck shell=bash
# Modul: Server-Binary installieren (aus Paket oder Go-Build)

module_binary_meta() {
  MODULE_ID=binary
  MODULE_NAME="Programm installieren"
  MODULE_DESC="Binary nach /usr/local/bin (Paket oder go build)"
}

module_binary_run() {
  module_binary_meta
  zp_log "── $MODULE_NAME ──"
  zp_require_root

  local pkg a src
  pkg="$(zp_package_dir)"
  a="$(zp_arch)"
  src=""

  if [ -x "$pkg/zeitprotokoll-server-linux-$a" ]; then
    src="$pkg/zeitprotokoll-server-linux-$a"
  elif [ -x "${ZP_INSTALL_SRC:-}" ]; then
    src="$ZP_INSTALL_SRC"
  else
    local repo
    repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
    if [ -x "$repo/bin/server" ]; then
      src="$repo/bin/server"
    elif command -v go >/dev/null 2>&1 && [ -f "$repo/go.mod" ]; then
      zp_log "  Baue Binary mit Go …"
      if [ "$ZP_INSTALL_DRY" = 1 ]; then
        zp_log "  [dry-run] go build -o $repo/bin/server ./cmd/server"
        src="$repo/bin/server"
      else
        mkdir -p "$repo/bin"
        ( cd "$repo" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/server ./cmd/server )
        src="$repo/bin/server"
      fi
    fi
  fi

  [ -n "$src" ] || zp_die "Kein Binary gefunden. Zuerst: cd server-go && scripts/package.sh   oder go build."

  if [ ! -x "$src" ] && [ "$ZP_INSTALL_DRY" != 1 ]; then
    zp_die "Binary nicht ausführbar: $src"
  fi

  local dest="$ZP_BIN"
  zp_log "  Quelle: $src"
  zp_log "  Ziel:   $dest"

  if [ -z "$ZP_INSTALL_ROOT" ]; then
    # Systembenutzer für den Dienst
    if ! id "$ZP_USER" >/dev/null 2>&1; then
      if [ "$ZP_INSTALL_DRY" = 1 ]; then
        zp_log "  [dry-run] useradd $ZP_USER"
      else
        useradd --system --no-create-home --shell /usr/sbin/nologin "$ZP_USER" 2>/dev/null \
          || useradd --system --no-create-home --shell /bin/false "$ZP_USER"
      fi
      zp_ok "Systembenutzer $ZP_USER"
    else
      zp_ok "Systembenutzer $ZP_USER vorhanden"
    fi
  fi

  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] install -m 0755 $src $dest"
  else
    mkdir -p "$(dirname "$dest")"
    # laufenden Dienst nicht mitten im Schreiben überschreiben → atomar
    local tmp="${dest}.new"
    cp "$src" "$tmp"
    chmod 0755 "$tmp"
    mv -f "$tmp" "$dest"
  fi
  zp_ok "Programm installiert"
  return 0
}
