# Stand und Plan (10.11.2026)

Chronologie und letzter Stand der Arbeit: `Verlauf.md`.

## Ziel
Das Zeitprotokoll (Einzelplatz: Mac, Browser-Oberfläche, Add-on, bucht nach Plane und Jira/Tempo) wird zum Team-Server, der Tempo Timesheets ablöst. Es hält die Zeiten selbst und genau; Plane und Jira sind Buchungsziele. Vier Oberflächen je Rolle: Administration, Mitarbeiter, Teamleiter, Personal. Konzept und Entscheidungen: `Gesamt-Spezifikation.md`, Vergleich ähnlicher Systeme: `Systemvergleich-und-Struktur.md`.

## Aufbau
- **Lokaler Tracker** (Node.js, React/Vite, Electron, Browser-Add-on): läuft unverändert weiter.
- **Team-Server** `server-go/` (Go, PostgreSQL, eine Programmdatei): Mehrbenutzer, Sitzungen, Rollen, Freigabe, Plane/Jira/Tempo. Liefert dieselbe React-Oberfläche aus (`scripts/build-web.sh`), im „Servermodus“ angepasst.
- **Betrieb:** lokal `scripts/dev.sh` (Postgres über Homebrew, Test auf Port 8090), Linux-Paket `scripts/package.sh` + `deploy/linux/install.sh` (systemd), OpenShift `Containerfile` + `deploy/openshift/`. Umzug/Cluster: `Betrieb-Migration.md`.

## Fertig (Go-Server)
Anmeldung: lokale Konten (Einzelbetrieb), Notfallzugang (nur Betrieb, nie Zeiten), LDAP/AD, OIDC (authentik/Keycloak/Entra). Rollen kombinierbar, Teams, Sichtbarkeit je Rolle. Monatsfreigabe mit Sperre in der Datenbank. Plane: Buchung als Summe je Ticket, Quellen, Tickets, Import (auf Anforderung). Jira Data Center + Tempo: mehrere Instanzen, Push, Ticket-Vorschlag (Light), Übergangs-Ticket. Arbeitsalltag: To-do, Arbeitstag, Monatsende/Lücken, Assistent (regelbasiert), alles abschaltbar (Vorschläge in vier Stufen). Admin-Oberfläche, Befehlszeile (`server help`), Selbsttest, Audit mit Hash-Kette. Sicherheit: mehrere unabhängige Reviews je Baustein, siehe `Sicherheit-Server-ISO27001.md`.

## Schnellstart und Auslieferung
- **Testen auf dem Mac:** `scripts/dev.sh` (Postgres per Homebrew, eigene Datenbank `zeitprotokoll_dev`, Schlüssel in `~/.zeitprotokoll-dev-key`, Port 8090); Befehle gegen dieselbe Datenbank: `scripts/dev.sh status|check|user …|team …|ldap …|oidc …`. Oberfläche bauen: `scripts/build-web.sh` (nach `server-go/web`, berührt `frontend/dist` des lokalen Trackers nicht). Erste Einrichtung unter `/setup` (Token steht im Log), Anmeldung `/login`; Administrator landet in `/admin`, Mitarbeiter in der Oberfläche `/`.
- **Linux (systemd):** `scripts/package.sh` baut `dist/zeitprotokoll-server-<version>.tar.gz` (amd64/arm64, mit Oberfläche); darin `install.sh` (Erstinstallation, `--upgrade`, `--uninstall [--purge]`), Dienstdatei mit Härtung, Konfigurationsvorlage. Nur syntaktisch geprüft, nicht auf einem Linux-Rechner gelaufen.
- **OpenShift:** `Containerfile`, `deploy/openshift/` (Deployment, Route, Netzwerkregeln …). Nicht gebaut/angewendet.
- **Bereinigter Export für GitLab:** `export-sanitized.sh` im Projektwurzelordner (enthält die echten Namen, nicht im Export); der Stand im GitLab-Projekt ist eine Momentaufnahme vom 10.11. und muss nach größeren Änderungen neu erzeugt werden.

## Zuletzt fertig (10.11.)
Monatsschluss nach einem Werktag, Nachtragen nur Teamleitung/Personal, Teams aus Verzeichnis oder lokal, Freigabe nur durch Personal, Übersichten und Bildschirme für Teamleiter und Personal, restliche Mitarbeiter-Routen. 19 Migrationen laufen lokal fehlerfrei. Nichts davon ist im Browser oder gegen echte Systeme abgenommen.

## Zuletzt fertig (Fortsetzung nach 10.11.)
- Oberfläche: Nachtragen-Dialog (Team/Personal, Begründung 10–500), „geschlossen“-Anzeige in der Monatsfreigabe, Hinweistexte und Tempo-Hash-Tests an Node angeglichen.
- E-Mail-Erinnerungen SMTP (abschaltbar, `/admin`, Migration 020 `notify_sent`, Inhalt ohne Zeiten) über `notifyApproval`.
- Tempo→lokal Abgleich (`GET/POST /api/tempo-sync`, Spiegel `source=tempo`, 12‑h-Automatik, Grundimport).
- Urlaubsantrag mit Genehmigung Teamleitung / Vertretung Personal (Migration 021 `leave_requests`, UI Abwesenheiten + Team/Personal).
- Geräte-Token + Client-Hinweise (Migration 021 `client_hints`, `device_tokens.kind`, UI Einstellungen › Erkennung; Ingest `POST /api/client/hints`).
- Tempo-Abwesenheitsvorschläge nach Abgleich (`is_absence` + `absence_hints`); gemeinsame Zähler Mehr-Pod (Migration 022 `rate_events`/`rate_blocks` für Login/Plane/Jira).
- Mehrere Plane-Workspaces (Migration 023/024/025): Admin `/admin` nur Adresse/Kennung (**ohne** Plane-Token); Nutzer Schlüssel je Host + Auswahl je Workspace; Scan, Tickets, Import, Push, Status und To-do über gewählte Workspaces; `plane_workspace_id` an Projekt/Aufgabe/Buchung; `plane_me_id` je Workspace.
- Web-Maske LDAP/OIDC in `/admin` (neben CLI); `server rekey`; DB-Rollen-Skript `deploy/postgres/roles.sql`.

## Offen (Reihenfolge)
1. Abnahme im Browser und gegen echte Systeme (Plane, Jira/Tempo, LDAP, OIDC, SMTP).
2. Exchange-OAuth/Graph und mehrere Mail-Konten (später).
3. Exporte (Lohn/Kunden; später entschieden).
4. Client: Mac-App spricht den Server an (nur lokale Erkennung: SSH, Meetings, Anwesenheit), Härtung, später Tray/Windows.
5. Jira Cloud / Tempo Cloud / Plane Cloud (nachrangig).

## Entscheidungen (Kurzfassung, Details in der Spezifikation)
Go + PostgreSQL; Plane per persönlichem Token; Jira Data Center + Cloud, Tempo nur als Brücke, Jira-Instanzen vom Admin; Server zuerst, Client danach; Freigabe ausschließlich Personal; Monatsschluss automatisch nach 1 Werktag, danach nachtragen nur Teamleitung/Personal; Tracken ohne Ticket erlaubt, Ticket-Vorschlag light und abschaltbar; technischer Admin sieht keine Zeiten; kein Import aus dem lokalen Tracker, Historie nur aus Tempo und Plane.

## Nicht getestet / Vorsicht
Datenbank-Teile und Migrationen 001–017 liefen lokal gegen PostgreSQL 16 nur beim Start (keine Funktionstests); Jira, Tempo, LDAP, OIDC, Exchange nie gegen echte Systeme; Oberfläche im Servermodus ohne Browser-Gegenprobe. Vor einem Pilot: Abnahme mit echten Konten, Penetrationstest, Betriebsvereinbarung, Sicherung/Wiederherstellung üben.
