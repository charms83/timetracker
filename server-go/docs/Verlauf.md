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

## Wo wir zuletzt waren
- **Läuft gerade (zwei Opus-Agenten):** (1) Monatsschluss nach einem Werktag, Nachtragen nur Teamleitung/Personal, Teams aus Verzeichnisgruppen oder lokal, Standard „Personal gibt frei“; (2) Übersichten für Teamleiter und Personal sowie restliche Mitarbeiter-Routen (Änderungsverlauf, Aufräumen, Ticket anlegen/starten). Nach dem Limit-Reset am Nachmittag fortgesetzt.
- **Server läuft lokal** (`scripts/dev.sh`, Port 8090) mit dem Stand von Schritt 2; Arbeitsstand danach noch nicht neu gebaut.
- **Dein nächster Test:** Browser `http://localhost:8090/`, Anmeldung als produktive Person, Plane-Schlüssel unter Einstellungen → Plane eintragen, Plane-Import mit Vorschau.

## Offene Rückfragen
- Logo und Icons im Export prüfen/ersetzen.
- Importierte Plane-Zeiten übers Übergangs-Ticket nach Tempo (Empfehlung: ja, nur nach Bestätigung je Monat)?
- Standard-Account/Plane-Modus in eingereichten Monaten sperren (Empfehlung: ja)?
- „Admin“ bei der Urlaubs-Vertretung = Personal (so angenommen)?
- Exporte (Lohn/Kunden) und Urlaubsantrag-Details; E-Mail-Technik (SMTP/Exchange).

## Als Nächstes (Reihenfolge)
1. Ergebnisse der zwei Agenten prüfen (Format, Tests, Server neu bauen, Migration 018 ff.), Opus-Review-Befunde.
2. Bildschirme Teamleiter (`/team`) und Personal (`/personal`) in der Oberfläche.
3. E-Mail-Erinnerungen, danach Urlaubsantrag mit Genehmigung.
4. Mehrere Plane-Workspaces; Tempo-Grundimport/Abgleich; Exporte.
5. Web-Maske LDAP/OIDC; Geräte-Token und Annahme-Schnittstelle; gemeinsame Zähler; DB-Rollen; `govulncheck`.
6. Sicherheits-Audit (Cloudflare-Skill) fortsetzen; Abnahme gegen echte Systeme; Client-Anbindung und Mac-Härtung; Cloud-Varianten.
