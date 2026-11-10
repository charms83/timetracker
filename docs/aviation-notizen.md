# Notizen: Aviation (forgejo.example.com/PLANE/plane-aviation)

Stand: Aviation 1.6.0. Nur aufgenommen, noch keine Anbindung gebaut.

## Was Aviation ist
- nginx-Reverse-Proxy vor Plane (`aviation-proxy`), injiziert Loader/Features in jede Plane-Seite.
- `aviation-api` (Node/Express/TS) mit Bot-Token (Plane-API) und direktem Zugriff auf die Plane-Postgres
  (Patroni, TLS erzwungen, CA-Bundle `bundled-certs.pem`, FQDN db-host-01.example.com).
- Endpunkte unter `/_aviation/api/*`: healthz, whoami (Plane-Session-Cookie → /users/me/), worklogs, …

## Relevante Mechanismen für Zeitprotokoll
1. **Anmeldung über Plane-Session**: `GET /_aviation/api/whoami` beweist die Plane-Session (OIDC/SSO).
   → Team-Server könnte hinter demselben Proxy laufen und die Identität so übernehmen.
2. **Worklogs in die Vergangenheit**: `POST /_aviation/api/worklogs` schreibt direkt in `issue_worklogs`
   (frei wählbares Datum, created_by = verifizierter User, Zod-Validierung, Rate-Limit, Audit).
3. **tempo-dual-log**: spiegelt nur über „+ Log“ erfasste Worklogs nach Tempo (Service-Account example-service).
   → Doppelbuchungen vermeiden, falls Zeitprotokoll UND Aviation nach Tempo schreiben.
4. **config/tempo-projects.json**: users (E-Mail → JIRAUSER-Key), projects[Plane-ID] → fallbackTicket + account.
   → Tempo verlangt den Account (workAttributeId 2, `_Account_`), sonst HTTP 400.
   Zeitprotokoll setzt den Account seit 6.1.0 ebenfalls (global + je Projekt).
5. **Betrieb**: Compose/Swarm/K8s neutral, Health `/_aviation/healthz`, Logs auf stdout, Secrets gitignored.

## Offene Ideen (später)
- Projekt-Zuordnung/Accounts aus tempo-projects.json übernehmen.
- Team-Server-Auth über Aviation/Plane-SSO statt eigener Benutzerverwaltung.
- Worklogs mit Vergangenheitsdatum über Aviation statt über die Plane-API.

## Entscheidung Team-Server (27.10.2026)
- Backend des Team-Servers in **Go** (Wunsch Team). Ziel: OpenShift, Red Hat, Debian; Browser-first.
- Die Mac-Version bleibt vorerst Node; API-Verträge (/api/*, /healthz, /readyz) als Vorlage für den Go-Server.
- Team-Server: offene, dokumentierte REST-API (Token-Auth) und Plugin-Fähigkeit (Module wie heute plane.js, jira.js, meetings.js – registrieren Routen, Einstellungen, Hintergrundjobs; einzeln abschaltbar). Jira ist nur Übergang (später alles über Plane, Zeitprotokoll ersetzt Tempo für die Abrechnung).

## Festgehalten 27.10.2026 – Ausrichtung Team-Server
- **Daten auf dem Server**: Alle Zeitdaten liegen zentral auf dem Server (Go-Backend, OpenShift/RHEL/Debian). Der Client (Browser, später Mac-App) hält lokal nur die aktuelle Woche vor (Offline/Schnelligkeit), alles Ältere kommt vom Server.
- **Anbindungen laufen über den Server**: Plane, Jira/Tempo, Kalender (Exchange/CalDAV) werden serverseitig angesprochen. Jeder Nutzer hinterlegt in seiner Ansicht seine eigenen Schlüssel/Tokens; der Server speichert sie verschlüsselt (pro Nutzer) und nutzt sie nur für dessen Aktionen.
- **Assistent** (Idee, noch nicht bauen): ein eingebauter Helfer, der beim Einrichten und sauberen Buchen unterstützt – z. B. Projekte/Tickets zuordnen, fehlende Zeiten vorschlagen, Unstimmigkeiten erklären, Schritt-für-Schritt-Einrichtung (Zugang, Zertifikat, Projekte).
- **Ticket-Ansicht** gibt es jetzt für Jira und Plane gleich aufgebaut (Suchen, Details, Kommentare, Status, Timer, Anlegen).
- **API & Plugins**: offene REST-API (Token), Module einzeln an-/abschaltbar.
