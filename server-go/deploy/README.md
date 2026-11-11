# Betrieb auf OpenShift

1. **Datenbank:** PostgreSQL (eigene VM oder vorhandener Server). Empfohlen: getrennte Rollen laut `deploy/postgres/roles.sql` (`zeitprotokoll_owner` für Migrationen, `zeitprotokoll` für Laufzeit); Verbindung mit `sslmode=verify-full`.
2. **Image bauen:** `cd server-go && podman build -t <registry>/example/zeitprotokoll-server:<tag> -f Containerfile .` und pushen. Tag fest vergeben.
3. **Secret anlegen** (Vorlage `deploy/openshift/secret.example.yaml`, Werte nie ins Repository): `DATABASE_URL`, `ZP_SECRET_KEY` (≥ 32 Zeichen, im Tresor sichern – ohne ihn sind alle gespeicherten Tokens unlesbar).
4. **Konfiguration anpassen:** `configmap.yaml` (`ZP_PUBLIC_URL`), `route.yaml` (Host), `deployment.yaml` (Image), `networkpolicy.yaml` (Ziele).
5. **Anwenden:** `oc apply -k deploy/openshift`.
6. **Erste Einrichtung:** Setup-Token steht im Pod-Log (`oc logs deploy/zeitprotokoll | grep -i token`), dann `https://<host>/setup`. Anmeldung: lokal (Einzelbetrieb), dann LDAP/AD oder authentik: Befehle im Pod `oc exec deploy/zeitprotokoll -- server ldap|oidc|team|user|breakglass …` (Hilfe: `server help`).
7. **Notfallzugang anlegen:** `oc exec deploy/zeitprotokoll -- server breakglass create` – Passwort in den Tresor.
8. **Prüfen:** `oc exec deploy/zeitprotokoll -- server check`.

Dateisystem ist schreibgeschützt, Prozess ohne Root und ohne Rechte; es gibt keine lokalen Daten (alles in PostgreSQL). Sicherung = Datenbank-Backup + `ZP_SECRET_KEY`.

Test, Umzug auf den Server und Cluster: siehe `docs/Betrieb-Migration.md`.

## Linux ohne OpenShift
`scripts/package.sh` baut das Paket, `deploy/linux/install.sh` installiert es als systemd-Dienst (siehe `docs/Stand-und-Plan.md`).
