# Plan: Server-Version für OpenShift

Stand 02.11.2026. Grundlage: `Zeitprotokoll Team-Server – Konzept.md`. Ziel: der heutige Node-Server läuft zentral auf OpenShift, die Mac-Apps gleichen dagegen ab. Offenes ist als „offen" markiert.

## Zielbild (03.11.2026)
Ein besseres Tempo für Plane: das heutige Zeitprotokoll als Server mit denselben Funktionen, mehrere Benutzer, Plane als Ticket-Quelle, Tempo/Jira nur als Übergang. Assistent (Vorschläge, Meetings, fehlende Zeit) bleibt. **Nicht** übernommen: Funktionen zum Aufräumen alter Monate/Altdaten (Rücksetzen, Bereinigen), Kunden-Verwaltung (Projekte kommen aus den Quellen; Kunden-API nur vorläufig wegen der Oberfläche). Offen: Umfang der Tempo-Ablösung (Abrechnung, Freigabe, Berichte).

## Richtungsentscheid (02.11.2026) – ersetzt die Node-Phasen unten
- **Server in Go** (eine statische Binary, gute Postgres-/OIDC-Bibliotheken, passt zu Container/OpenShift). Rust bleibt Option, wenn später Leistung oder Speichersicherheit es verlangen – kein Grund dafür heute.
- **Client wie heute:** Electron-Mac-App + Browser-Add-on + dieselbe React-Weboberfläche, vom Go-Server ausgeliefert.
- **Buchungs-Logik** (Übergangs-/Sammeltickets, Plane-Summen je Work-Item, Tempo-Push, Monatsende-Ablauf) wird portiert, aber **abschaltbar** je Installation/Person; Standard: Zeit direkt aufs Ticket. **Nachbuchen/Nachtragen bleibt.**
- **Node-System läuft parallel weiter** (nur Fehlerkorrekturen). Neuer Code in `server-go/`. Datenimport aus SQLite am Ende.
- Reihenfolge: Go-Server (Postgres, OIDC, Profil-Tokens, Buchungen, Abgleich) → Anpassung des Clients. PostgreSQL, SSO und Tokens wie unten beschrieben.
- Die Phasen 1a/1b unten (Node auf Postgres umstellen) entfallen; 1c, 2, 3, 4 gelten sinngemäß für den Go-Server.

## Ausgangslage (aus dem Code)
- Ein Prozess (`backend/server.js`, Port 3747), SQLite-Datei, Einstellungen und Schlüssel für Plane/Jira/Tempo pro Mac, keine Benutzer, kein Login.
- Mac-spezifisch und nicht in den Container: SSH-Log, Kalender-/Meeting-Erkennung, Anwesenheit (`ssh-watch.js`, `meetings.js`, `presence.js`, `desktop/`). Bleibt lokal in der App.
- Web-Oberfläche (`frontend/dist`) wird heute vom Server ausgeliefert, das kann so bleiben.

## Entschieden (02.11.2026)
- Datenbank: **PostgreSQL**.
- Anmeldung: **SSO über OIDC** (authentik).
- Jira/Tempo/Plane: **kein zentrales Dienst-Konto.** Jede Person trägt selbst auf der Profilseite ein: ihren Jira-Token (gilt auch für Tempo, Tempo läuft über Jira Data Center, kein eigener Tempo-Token) und ihren Plane-API-Schlüssel (wie heute in den Einstellungen), serverseitig verschlüsselt je Benutzer gespeichert. Die Mac-App braucht keine Tokens mehr lokal.
- Weiter **offen**: OIDC-Issuer/Client-ID und Gruppen→Rollen, Hostname/TLS, Registry fürs Image, Postgres-Betrieb (OpenShift-Operator, eigene VM oder vorhandener DB-Server).

## Hauptrisiko: Postgres
Der Code nutzt `node:sqlite` **synchron** (`one/all/run`, rund 260 Aufrufe allein in `server.js`, dazu `admin.js`, `monthend.js`, `plane.js`, `tempo-*.js`, `sources.js` u. a.). Postgres ist asynchron, also müssen alle Aufrufstellen auf `await` umgestellt und SQL-Unterschiede angepasst werden (`INSERT OR REPLACE`, `datetime('now')`, `AUTOINCREMENT`, `PRAGMA`, `lastInsertRowid`). Das ist der größte Posten und gehört in Phase 1b. Alternative zur Entlastung: erst SQLite+PVC ausrollen (Phase 1a), Postgres danach – dann aber doppelte Arbeit bei der Datenmigration. **Empfehlung:** direkt Postgres, aber DB-Zugriff zuerst hinter eine Schicht (`db.js`) ziehen, dann Modul für Modul umstellen.

## Phase 1a – Container-lauffähig
- Konfiguration über Umgebungsvariablen: Port, `DATABASE_URL`, `DATA_DIR`, Bind 0.0.0.0, Schlüssel nicht mehr aus Env/Datei einer Person.
- Mac-Module abschaltbar per `ZP_MODE=server` (SSH-Watch, Meetings, Presence, Selbst-Update/Build in `admin.js` aus).
- `GET /healthz`, Logging nach stdout, sauberes SIGTERM.
- `Containerfile`: Multi-Stage, `ubi9/nodejs-22-minimal` (node:sqlite entfällt mit Postgres, `pg` als neue Abhängigkeit – **Rückfrage nötig**), läuft als beliebige UID.

## Phase 1b – Postgres-Umstellung
- `db.js` mit async `one/all/run/tx`, Migrationen als nummerierte SQL-Dateien (heute `CREATE TABLE IF NOT EXISTS` und `ALTER` im Code).
- Alle Module umstellen, SQL angleichen, Einmal-Import der bestehenden SQLite-Datei (pro Mac, später auch für die Pilotgruppe).
- Fertig wenn: Server startet gegen leere Postgres, alle Ansichten laden, Buchen/Timer/Nachtragen/Monatsabschluss getestet (Abnahme durch dich). Review: `reviewer` mit Opus (Datenintegrität).

## Phase 1c – Benutzer, SSO, Profil-Tokens
- Tabellen `users`, `user_secrets` (verschlüsselt, Schlüssel aus OpenShift-Secret), `user_id` an allen Nutzerdaten (Buchungen, Einstellungen, Regeln, Abwesenheiten, To-dos).
- OIDC-Login (Authorization-Code mit PKCE), Sitzungs-Cookie; für die Mac-App/Add-on ein widerrufbares Geräte-Token.
- Profilseite: eigenen Jira-Token (für Jira und Tempo) und Plane-Schlüssel eintragen, testen, löschen. Plane/Jira/Tempo-Aufrufe laufen mit dem Token der jeweiligen Person (`plane.js`, `jira.js`, `tempo-*.js` heute globale Einstellungen → je Benutzer).
- Rechte serverseitig prüfen, Audit-Log. Review: `reviewer` mit Opus (Sessions, Krypto, Rechte).

## Phase 2 – OpenShift-Manifeste (`deploy/openshift/`)
- Deployment (mehrere Replicas möglich, sobald Sitzungen in Postgres liegen), Service, Route (TLS edge), PVC `/data`, ConfigMap, Secret (Vorlage ohne Werte: DB-URL, OIDC-Client, Verschlüsselungsschlüssel), NetworkPolicy, Resource-Requests/Limits, Probes.
- Tägliche DB-Sicherung per CronJob (`pg_dump` → PBS-Ziel, Konzept A.8.13; entfällt, wenn der DB-Betreiber sichert).
- Deployment-Anleitung `docs/OpenShift-Deploy.md` (Build, Push, apply, Rollback).
- Fertig wenn: Test-Namespace erreichbar über Route, Rollout und Rollback getestet (Abnahme durch dich).

## Phase 3 – Team-Funktionen (laut Konzept)
- Teams/Rollen aus authentik-Gruppen.
- Buchungen mit `uuid`, `version`, `updated_at`, `user_id`; Abgleich-Endpunkte App↔Server (Konfliktregel: höhere Version gewinnt).
- Monatsstatus (offen/eingereicht/freigegeben), Sperre nach Freigabe.
- Review durch Opus (Freigabe mit Rechtswirkung).

## Phase 4 – App-Anbindung
- Mac-App/Add-on: Server-URL, SSO-Anmeldung, Abgleich; Offline-Betrieb bleibt, lokale Tokens entfallen.
- Pilot (3 Personen, 1 Monat) erst nach Betriebsvereinbarung.

## Nächster Schritt
Phase 1a (Container, Env-Konfiguration, `healthz`) und parallel Planung von `db.js` für 1b. Neue Abhängigkeit `pg` (und für OIDC `openid-client`) brauche ich freigegeben.
