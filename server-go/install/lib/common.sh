# shellcheck shell=bash
# Gemeinsame Hilfen für den modularen Server-Installer.
# Kein Ansible/Semaphore – reine Shell mit Auswahldialogen (UEBERGABE).

: "${ZP_INSTALL_ROOT:=}"
: "${ZP_INSTALL_QUIET:=0}"
: "${ZP_INSTALL_DRY:=0}"
: "${ZP_INSTALL_YES:=0}"

ZP_BIN="${ZP_INSTALL_ROOT}/usr/local/bin/zeitprotokoll-server"
ZP_CONF_DIR="${ZP_INSTALL_ROOT}/etc/zeitprotokoll"
ZP_ENV="${ZP_CONF_DIR}/zeitprotokoll.env"
ZP_WEB="${ZP_INSTALL_ROOT}/usr/share/zeitprotokoll/web"
ZP_UNIT_DIR="${ZP_INSTALL_ROOT}/etc/systemd/system"
ZP_UNIT="${ZP_UNIT_DIR}/zeitprotokoll.service"
ZP_USER="${ZP_USER:-zeitprotokoll}"
ZP_DB_NAME="${ZP_DB_NAME:-zeitprotokoll}"
ZP_DB_USER="${ZP_DB_USER:-zeitprotokoll}"

zp_log()  { [ "$ZP_INSTALL_QUIET" = 1 ] || printf '%s\n' "$*"; }
zp_ok()   { zp_log "  ✓ $*"; }
zp_warn() { zp_log "  ! $*"; }
zp_err()  { printf '  ✗ %s\n' "$*" >&2; }
zp_die()  { zp_err "$*"; exit 1; }

zp_need_cmd() {
  command -v "$1" >/dev/null 2>&1 || zp_die "Befehl fehlt: $1"
}

zp_is_root() {
  [ "$(id -u)" -eq 0 ]
}

zp_require_root() {
  [ -n "$ZP_INSTALL_ROOT" ] && return 0
  zp_is_root || zp_die "Bitte als root ausführen (oder ZP_INSTALL_ROOT für einen Testpfad setzen)."
}

zp_arch() {
  local m
  m="$(uname -m)"
  case "$m" in
    x86_64) echo amd64 ;;
    aarch64|arm64) echo arm64 ;;
    *) zp_die "Architektur $m nicht unterstützt (nur amd64/arm64)" ;;
  esac
}

zp_package_dir() {
  # Verzeichnis mit Binary/web aus dem tar.gz oder neben diesem Installer
  if [ -n "${ZP_PACKAGE_DIR:-}" ] && [ -d "$ZP_PACKAGE_DIR" ]; then
    echo "$ZP_PACKAGE_DIR"
    return
  fi
  # install/ liegt im Repo unter server-go/install → Paket kann deploy/linux + gebautes Binary sein
  local here
  here="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  if [ -x "$here/zeitprotokoll-server-linux-$(zp_arch)" ]; then
    echo "$here"
    return
  fi
  if [ -d "$here/deploy/linux" ]; then
    echo "$here/deploy/linux"
    return
  fi
  echo "$here"
}

zp_run() {
  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] $*"
    return 0
  fi
  "$@"
}

zp_write() {
  # zp_write <mode> <path>  (Inhalt von stdin)
  local mode="$1" path="$2" dir
  dir="$(dirname "$path")"
  if [ "$ZP_INSTALL_DRY" = 1 ]; then
    zp_log "  [dry-run] write $path (mode $mode)"
    cat >/dev/null
    return 0
  fi
  mkdir -p "$dir"
  cat > "$path"
  chmod "$mode" "$path"
}

zp_gen_key() {
  head -c 48 /dev/urandom | base64 | tr -d '\n=+/' | head -c 48
}

zp_confirm() {
  local q="$1"
  if [ "$ZP_INSTALL_YES" = 1 ]; then return 0; fi
  local a
  printf '%s [j/N] ' "$q"
  read -r a || return 1
  case "$a" in j|J|y|Y|ja|Ja|yes|Yes) return 0 ;; *) return 1 ;; esac
}
