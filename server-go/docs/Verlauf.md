# Verlauf: was bisher gemacht wurde und wo wir zuletzt waren (Stand 10.11.2026, Abend)

Kurzer Überblick: `Stand-und-Plan.md`. Entscheidungen: `Gesamt-Spezifikation.md` (besonders Abschnitt 16). Diese Datei ist das Arbeitsprotokoll, geordnet nach Tagen.

## Bis 03.11. – Grundlage des Go-Servers
- Entscheidung für Go + PostgreSQL (kleine Container für OpenShift); lokaler Tracker (Node/Electron) läuft parallel unverändert.
- Gebaut: Einrichtung (`/setup`), verschlüsselte Zugangsdaten je Person, Stammdaten, Zeiteinträge mit Plausibilitätsprüfung, Timer/Pause, Konto (Soll/Ist/Saldo), Abwesenheiten/Feiertage, Planung, Berichte/Stundenzettel/Raster, Plane-Anbindung (Bremse, Summen-Worklog je Ticket), Quellen, Admin-Oberfläche, Befehlszeile, Selbsttest.
- Plane Ende-zu-Ende geprüft (Ticket, 5 Minuten buchen, nach Plane übertragen, erneut = keine Änderung, Löschen = entfernt).

## 04.11. – Sicherheit, Konten, Anmeldung
- Sicherheitshärtung nach mehreren unabhängigen Opus-Reviews (Plane-Umleitungen, Doppelanlage, Einrichtungs-Token, Proxy-Adressen, Mandantentrennung, Notfallzugang ohne Zeitdaten u. a.), Dokument `Sicherheit-Server-ISO27001.md`.
- Konten: lokales Testkonto + eine produktive Person (Einzelbetrieb), Notfallzugang (nur Betrieb, nur per Befehl scharf), Anmeldeschutz ohne Benutzer-Aufzählung.
- LDAP/AD (go-ldap, LDAPS/StartTLS, Gruppen, 30-min-Nachprüfung) und OIDC (authentik/Keycloak/Entra, PKCE, state/nonce), beide Opus-geprüft, je ein Review-Durchlauf mit Korrekturen.

## 05.–09.11. – Rollen, Freigabe, Jira/Tempo, Betrieb
- Rollen kombinierbar (Mitarbeiter, Teamleiter, Personal), Teams, Sichtbarkeit nur nach Rolle; Monatsfreigabe (einreichen/freigeben/ablehnen/wiedereröffnen) mit Sperre in der Datenbank; Admin-Reiter Teams.
- Jira Data Center + Tempo Timesheets DC nach der Node-Spezifikation (`Jira-Tempo-Spezifikation.md`): Push mit Schutz gegen Doppelbuchung, Monatssperre, mehrere Jira-Instanzen (Admin legt fest, Token je Person und Instanz), Ticket-Vorschlag (Light, dreifach abschaltbar), bestätigtes Ticket-Anlegen, Übergangs-Ticket je Monat.
- Plane-Import (ungenaue Zeiten, auf Anforderung, ohne Doppelte).
- Betrieb: `Containerfile`, OpenShift-Manifeste, Linux-Installer (`scripts/package.sh`, systemd), Migrationspfad/Cluster-Hinweise (`Betrieb-Migration.md`), Migrations-Sperre und Schutz „Datenbank neuer als Programm“.
- Leitentscheidungen: Zeitprotokoll löst Tempo ab (Tempo nur Brücke), hält die Zeiten selbst und genau, Plane per persönlichem Token, Server zuerst – Client zuletzt, Cloud nachrangig, kein Import aus dem lokalen Tracker.
- Paritätsliste Oberfläche ↔ Go-Server (`Paritaet-Oberflaeche.md`): 86 Pfade der alten Oberfläche, 49 fehlten; Schritte 1, 1b (Jira/Plane) und 2 (To-do, Arbeitstag, Monatsende, Lücken, Assistent, Schätzungen) gebaut.

## 09.–10.11. – Erste echte Umgebung und Oberfläche
- Lokale Testumgebung `scripts/dev.sh` (Postgres 16 Homebrew): alle 17 Migrationen laufen von Grund auf fehlerfrei.
- Einrichtung und Anmeldung im Browser getestet. Gefundener und behobener Fehler: Die Anmeldeseite wurde durch die Sicherheitsregeln (CSP) blockiert; ebenso falsche Weiterleitung nach dem Login.
- Trennung Admin / Mitarbeiter: reines Administrationskonto darf nur die Verwaltung; „Mein Profil“ für Mitarbeiter; Vorschläge/Zugangsdaten nicht beim Admin-Konto.
- Alte React-Oberfläche läuft im „Servermodus“ am Go-Server (`scripts/build-web.sh`): Anmeldung, Einstellungen je Rolle, Monat einreichen, Plane-Import, „braucht Client“-Hinweise, Fehlermeldungen bei gesperrtem Monat. Im Browser noch nicht abgenommen.
- Systemvergleich (Tempo, Kimai, Clockify, Toggl, Harvest, Plane, Jira) und Strukturvorschlag je Oberfläche (`Systemvergleich-und-Struktur.md`); Entscheidungen zu acht Fragen (Spezifikation Abschnitt 16).
- Cloudflare-„security-audit“-Skill installiert (global). Der volle Audit von `server-go` lief an, brach am 5-Stunden-Limit ab; die Vorarbeit liegt im Arbeitsordner der Sitzung, der Lauf ist **noch nicht abgeschlossen**. Der „apple-design“-Skill wurde vom Berechtigungssystem blockiert (Repository einer Einzelperson), nicht installiert.
- Bereinigter Export ohne Firmen-/Personen-/Hostnamen nach GitLab (`opentracker-group1/OpenTracker-project`, privat zu halten): lokaler Tracker, Team-Server, Konzept, Dokumente. Skript `export-sanitized.sh` (enthält die echten Namen, nicht im Export). Stand dort: Mittag des 10.11.

## 10.11. Nachmittag/Abend – Monatsschluss, Nachtragen, Teams, Übersichten
- **Fertig und geprüft (Opus-Review je Baustein):** automatischer Monatsschluss nach einem Werktag (Einstellung `month_grace_workdays`), Nachtragen danach nur durch Teamleitung/Personal mit Begründung und Protokoll (Trigger-Bypass kontrolliert, Hash-Kette mit Akteur), Teams aus Verzeichnisgruppen (`zp-team-<name>`, `-lead`, optional `ldap_team_base`) oder lokal, Freigabe nur durch Personal (Standard `hr`, Selbstfreigabe nur für die einzige Person im Personal), Übersichten für Teamleiter und Personal, restliche Mitarbeiter-Routen (Änderungsverlauf mit Hash-Prüfung, Aufräumen, Dubletten, Ticket anlegen/starten).
- **Oberfläche:** Bereiche „Team“ und „Personal“ gebaut (Navigation je Rolle, Übersicht, Personen-Detail, Freigeben/Ablehnen/Wiedereröffnen). Im Browser noch nicht abgenommen; Nachtragen-Dialog, „geschlossen“-Anzeige und neue Hinweise fehlen noch.
- **Datenbank:** Migrationen 018 und 019 laufen auf der lokalen PostgreSQL-16-Datenbank fehlerfrei (insgesamt 19 Migrationen).
- **Ziel des Projekts:** GitHub `charms83/timetracker` (privat zu halten); davor GitLab-Probe. Bereinigter Export ohne Firmen-, Personen- und Hostnamen, ohne „Aviation“ (Skript `export-sanitized.sh`, behält den Verlauf).

## Wo wir zuletzt waren
- **Server läuft lokal** (`scripts/dev.sh`, Port 8090) mit allen 19 Migrationen; Oberfläche mit Team/Personal ist gebaut (`scripts/build-web.sh`).
- **Alle Agenten sind fertig.** Es läuft nichts mehr im Hintergrund; der Nutzer hat bewusst um eine Pause gebeten.
- **Nächster Test (Nutzer):** Anmelden, Plane-Schlüssel eintragen, Plane-Import mit Vorschau, Monat einreichen, Team-/Personal-Bereich ansehen. Fehler als Meldung oder Screenshot zurückgeben.

## Offene Rückfragen
- Logo und Icons im Export prüfen/ersetzen.
- Importierte Plane-Zeiten übers Übergangs-Ticket nach Tempo (Empfehlung: ja, nur nach Bestätigung je Monat)?
- Standard-Account/Plane-Modus in eingereichten Monaten sperren (Empfehlung: ja)?
- `ldap_team_base` setzen (Empfehlung des Reviews), Logo/Icons im Export prüfen.
- „Admin“ bei der Urlaubs-Vertretung = Personal (so angenommen)?
- Exporte (Lohn/Kunden) und Urlaubsantrag-Details; E-Mail-Technik (SMTP/Exchange).

## Weiterbau (nach dem bereinigten Export)
- Code-Review: Go-Server weitgehend fertig und getestet (Unit), Oberfläche hatte Platzhalter für Nachtragen; Tempo-Hash-Erwartungswerte waren veraltet (U+2028 in modernem `JSON.stringify`).
- Gebaut: Nachtragen-UI in Team/Personal (`zp-people.jsx`), „geschlossen“ in Monatsfreigabe (`zp-servermode.jsx`), klarere Sperr-Hinweise (`zp-ui.jsx`), SMTP-E-Mail über `notifyApproval` (`notify_mail.go`, Migration 020, Admin-Einstellungen).
- Tests: `go test ./…` grün (inkl. angeglichene Tempo-Hashes und Mail-Hilfstests).

## Weiterbau (Tempo / Urlaub / Geräte)
- Tempo→lokal: `tempo_sync.go` (GET/POST `/api/tempo-sync`, 12‑h-Cron, Migration nutzt bestehende `time_entries.source='tempo'`).
- Urlaubsantrag: Migration 021 `leave_requests`, `leave.go`, UI `zp-leave.jsx` (Abwesenheiten + Team/Personal).
- Geräte-Token + Client-Hinweise: Migration 021 `client_hints` / `device_tokens.kind`, `devices.go`, UI `zp-devices.jsx`.
- Tests: `go test ./internal/app` grün (inkl. Hilfstests Monatfenster / Leave-Regel / Token-Hash).

## Weiterbau (Plane-Workspaces / Admin-Auth / Rekey)
- Migration 023 `plane_workspaces`; Admin-CRUD, Nutzer-Profil je Host; Quellen/Scan über mehrere Workspaces; Primär-Spiegel in `server_config`.
- Migration 024: `plane_workspace_id` an projects/tasks/time_entries; Sync/Tickets/Import/Push/Status/To-do auflösen per Workspace (`?workspace=` oder Projekt/Ticket); `plane_me_id:<id>`.
- `/admin`: LDAP/OIDC-Maske (`admin_auth.go`) inkl. LDAP-Test; Secrets nie in GET.
- `server rekey` (`RekeySecrets` + CLI); DB-Rollen `deploy/postgres/roles.sql`.
- Exchange/Multi-Mail bewusst zurückgestellt.

## Weiterbau (Admin ohne Doppelrolle / GUI-Trennung)
- Entscheidung umgesetzt: technischer Admin = getrenntes Konto (LDAP/OIDC-Admin-Gruppe ok), **keine** Doppelrolle mit Zeiterfassung.
- Migration 026 setzt `productive=false` für alle `role=admin`; `timeAccount()` / `tracksTime()` schließen Admin und Notfallzugang aus; Provision/Nachprüfung/Admin-API erzwingen das.
- Login/OIDC und React-`isAdminOnly` leiten Admin nur nach `/admin`; Nutzer-GUI `/` ohne Admin-Link; Team-/Monat-/Sync-Filter ohne Admin-Konten.
- Docs: Spezifikation §4 und Stand-und-Plan angepasst.

## GUI-Stand + Bugbot (11.11.)
- **Rollen-Gates:** Admin `/admin`; Mitarbeiter `/`; Lead `#team`; HR `#personal`; Mischrollen additive Nav – Code fertig, Browser-Abnahme offen; Personal-Exporte offen.
- **Bugbot** (Branch-Review): behoben – LDAP-Rollenwechsel beendet Sitzungen immer; Urlaub `days` nur serverseitig; Ablehnen-Prompt Abbrechen; Mobile-Nav = Desktop-Filter; `loadMe` leitet nach Admin um.
- Spez-Statuszeilen (Oberfläche, LDAP-Webmaske, Team/Personal, SMTP) nachgezogen.

## Weiterbau (Auswertung Team/Personal, kein Lohn-Export)
- CSV der Monatsübersicht (Team + Personal) und Buchungen je Person – Auswertung, **ohne** „exportiert“-Sperre (Spez §16.7 weiter offen).
- Personen-Detail: Buchungsprotokoll via `GET /api/audit?user=` (asTarget); Admin-Konten nicht per `?user=` einsehbar.

## Weiterbau (Parität Tempo/Assistent + Client→Server)
- Assistent-Aufgaben: `tempo_sync`, `duplicates`, `cleanup`, `tempo_noticket` (wie Node); Sync-UI-Texte serverfähig; CSV bleibt lokal, Server = API-Abgleich.
- Mac-Client: `mode=team` + `serverUrl` + `deviceToken` → UI am Go-Server, Presence/Meetings als `client_hints`; Doku `desktop/TEAM-SERVER.md`.
- Sicherheit: Geräte-Token nicht für Admin; Create nur zeiterfassende Konten.

## Client-Sicherheit (Review + Härtung)
- Medium behoben: Device-Token nur an `/api/*` (webRequest); Team-`serverUrl` nur `https` (localhost-HTTP mit `allowHttpTeam`); `desktop.json` Mode `0600`.
- Server: Hints-Ingest per Geräte-Token rate-limitiert (60/min), max. 50 Hints, Payload ≤ 4 KiB; Admin/Breakglass weiter ausgeschlossen.
- Token-Ablage: Client `safeStorage` (Keychain/DPAPI) in `device-token.bin`, nie Klartext in `desktop.json`; Server HMAC-SHA256 mit `ZP_SECRET_KEY` (`v1:`), Legacy-SHA256 wird bei Nutzung migriert.
- Team-Client-Auth: **Login-Sitzung auf dem Server** (Cookie via `session.fetch`); Geräte-Token nur noch optional (Add-on/curl). UI im Electron-Fenster, nicht Systembrowser.
- Verbindung gehärtet: Client lehnt TLS-Fehler ab, blockiert HTTP-Downgrade, optional Cert-Pin; Server `__Host-zp_session` + Session-HMAC, Betrieb braucht `ZP_PUBLIC_URL=https://`.
- Usability: Cert-Pin nur wenn gesetzt (Firmen-CA bleibt nutzbar); Notifications erlaubt; Alltag = URL setzen + anmelden.
- Server-Update: Version per ldflags; `GET /api/admin/update` + Admin-Tab; `install.sh --upgrade` mit Backup; CLI `version`; Doku `Betrieb-Update.md` (kein In-App-Upload).
- Abrechnungs-API: Integrations-Token `zpint_…` (Admin, Migration 028), `GET /api/integrations/projects` (+ entries) für Stunden auf Projekten/Tickets, plus Lohn (`/payroll`); Geräte-Token wieder nur Client-Hinweise; Doku/UI korrigiert.
- Tracken ohne Ticket: Timer ohne Projekt/Ticket starten, später zuweisen (`POST /api/timer/assign`); Dauerbuchung wie Tempo (nur Stunden) → Server legt Uhrzeit in freie Lücke (`time_precise=false`).
- User „Daten prüfen“ auch im Servermodus (Einstellungen › Allgemein); Überschneidungen bleiben unter Heute – Assistent nicht vermischt. Mini/Tray: Team-Server-Session für Timer.
- Team-Anwesenheit: Mac schreibt `POST /presence` (Session) bzw. Hint→`presence_min`; Tempo-Outbox `gaps` aus `dayOverview` (wie Node).
- Offen bleibt: „exportiert“-Sperre/Formate; Add-on Server-URL; echte Systemtests.

## Als Nächstes (Reihenfolge)
1. Abnahme im Browser und gegen echte Systeme (Plane, Jira/Tempo, LDAP, authentik, SMTP); gefundene Fehler beheben.
2. Exchange-OAuth/Graph und mehrere Mail-Konten.
3. Exporte (Lohn/Kunden).
4. `govulncheck` regelmäßig; Sicherheits-Audit fortsetzen; Mac-Schlüsselbund / Tray-UI; Cloud-Varianten.
