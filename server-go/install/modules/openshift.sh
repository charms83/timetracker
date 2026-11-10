# shellcheck shell=bash
# Modul: OpenShift – Anleitung / optionales oc apply -k

module_openshift_meta() {
  MODULE_ID=openshift
  MODULE_NAME="OpenShift"
  MODULE_DESC="Manifeste prüfen / optional anwenden (kein Ansible)"
}

module_openshift_run() {
  module_openshift_meta
  zp_log "── $MODULE_NAME ──"

  local repo
  repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
  local dir="$repo/deploy/openshift"
  [ -d "$dir" ] || zp_die "deploy/openshift fehlt"

  zp_ok "Manifeste: $dir"
  for f in kustomization.yaml deployment.yaml service.yaml route.yaml configmap.yaml \
           networkpolicy.yaml poddisruptionbudget.yaml secret.example.yaml; do
    if [ -f "$dir/$f" ]; then zp_ok "$f"
    else zp_warn "fehlt: $f"; fi
  done

  cat <<EOF

  Ablauf (Auswahldialog):
  1. Image bauen:  cd server-go && podman build -t <registry>/zeitprotokoll-server:<tag> -f Containerfile .
  2. Secret aus secret.example.yaml (DATABASE_URL, ZP_SECRET_KEY) – Werte nie ins Repo
  3. configmap.yaml / route.yaml / deployment.yaml anpassen
  4. oc apply -k deploy/openshift
  5. Token: oc logs deploy/zeitprotokoll | grep -i token → /setup
  Details: deploy/README.md und docs/Betrieb-Migration.md

EOF

  if [ "$ZP_INSTALL_YES" = 1 ] && [ "${ZP_OPENSHIFT_APPLY:-0}" = 1 ]; then
    zp_need_cmd oc
    if [ "$ZP_INSTALL_DRY" = 1 ]; then
      zp_log "  [dry-run] oc apply -k $dir"
    else
      zp_run oc apply -k "$dir"
      zp_ok "oc apply ausgeführt"
    fi
    return 0
  fi

  if [ "$ZP_INSTALL_YES" != 1 ]; then
    if zp_confirm "Jetzt oc apply -k ausführen? (Secret muss existieren)"; then
      zp_need_cmd oc
      if [ "$ZP_INSTALL_DRY" = 1 ]; then
        zp_log "  [dry-run] oc apply -k $dir"
      else
        zp_run oc apply -k "$dir"
        zp_ok "oc apply ausgeführt"
      fi
    else
      zp_ok "Nur Anleitung – nichts angewendet"
    fi
  fi
  return 0
}
