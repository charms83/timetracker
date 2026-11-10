# Sicherheit des Zeitprotokoll-Servers – Maßnahmen und ISO 27001

Stand 03.11.2026. Geltungsbereich: `server-go/` (Go-Server, PostgreSQL, OpenShift). Grundlage: internes Sicherheits-Audit (Opus-Review, 03.11.2026) und Gegenprüfung nach der Härtung. Zuordnung zu ISO/IEC 27001:2022 Anhang A. **Offen** ist als „offen“ markiert.

## Umgesetzt

| Maßnahme | Beschreibung | Kontrolle |
|---|---|---|
| Mandantentrennung | Jede Abfrage auf Personendaten filtert auf `user_id`; fremde Ids werden geprüft (`owned`) | A.5.15, A.8.3 |
| Zugangsdaten verschlüsselt | Jira-Token und Plane-Schlüssel mit AES-256-GCM, gebunden an Person und Art (AAD), der Plane-Schlüssel zusätzlich an die Plane-Adresse (Schema + Host); Schlüssel nur aus dem Secret (`ZP_SECRET_KEY`, ≥ 32 Zeichen, im Betrieb Pflicht); alte Chiffrate werden vom Selbsttest neu verschlüsselt | A.8.24, A.5.17 |
| Aufrufer-Adresse | Hinter dem Router nur mit `ZP_TRUST_PROXY=1` und `ZP_TRUSTED_PROXIES` (Zählung von rechts in `X-Forwarded-For`, nicht fälschbar); sonst gilt die Verbindungsadresse | A.8.20, A.8.16 |
| Kein Proxy für Plane | Der Server verbindet sich direkt; Zieladress-Sperre (Link-Local, Metadaten-Adressen) wirkt dadurch auch nach DNS-Auflösung | A.8.20, A.8.22 |
| Schlüssel nie im Klartext ausgegeben | Antworten zeigen nur die letzten 4 Zeichen; Logs, Audit und Fehlermeldungen ohne Geheimnisse | A.8.15, A.5.33 |
| Plane-Adresse geschützt | Nur Administrator oder Einrichtung (mit Token); https Pflicht, keine Zugangsdaten in der Adresse, keine Umleitungen, Link-Local gesperrt; **bei Host-Wechsel werden alle Plane-Schlüssel gelöscht** | A.8.20, A.8.22, A.8.26 |
| Einrichtung gesperrt | `/setup` nach Abschluss gesperrt (außer `ZP_SETUP_ENABLED=1`); Fehlversuche begrenzt (5 je 15 Min); alles wird vor dem Speichern geprüft; Token nur im Log, solange die Einrichtung offen ist | A.8.2, A.8.5 |
| Testmodus verboten im Betrieb | `auth_mode none` lehnt der Server ab bei `ZP_PRODUCTION=1` oder auf Kubernetes/OpenShift; sonst nur localhost mit Host-Prüfung (gegen DNS-Rebinding) | A.8.5, A.8.9 |
| Schutz vor fremden Webseiten | Origin-Prüfung bei ändernden Aufrufen, Host-Prüfung gegen `ZP_PUBLIC_URL` | A.8.26 |
| Sicherheits-Köpfe | CSP, `X-Content-Type-Options`, `X-Frame-Options`/`frame-ancestors`, Referrer-Policy, HSTS, `Cache-Control: no-store` auf der API | A.8.26, A.8.12 |
| Begrenzung | Anfragen je Adresse (30/s, Spitze 120), Server-Timeouts, Header-Größe, Body-Größe, Datenbank-`statement_timeout` und Pool | A.8.6, A.8.20 |
| Absturzschutz | Hintergrundaufgaben fangen Abstürze ab, der Server läuft weiter | A.8.6, A.8.14 |
| Änderungsprotokoll | Zeiteinträge: Datenbank-Trigger mit Hash-Kette (`entry_audit`), nur anhängen; `audit_log` gegen Ändern/Löschen geschützt; Admin-Aktionen und Befehle werden protokolliert | A.8.15, A.5.28 |
| Rollen | `user`/`lead`/`admin`; immer mindestens ein aktiver Administrator (mit Sperre gegen Rennen); gesperrte Personen werden abgewiesen | A.5.15, A.5.18 |
| Selbsttest | Beim Start und alle 6 Stunden; prüft Datenbank, Migrationen, Tabellen, Schlüssel, Zuordnung der Daten, Administrator, Testmodus; sichere Reparaturen automatisch | A.8.8, A.8.14 |
| Befehle gegen Fehlbedienung | `check --force` löscht Zugangsdaten nur mit gesetztem `ZP_SECRET_KEY`, nie über das Web; Befehle legen nie einen Schlüssel an | A.8.2, A.8.32 |
| Auslieferung | Kein Verzeichnislisting, keine versteckten Dateien; `.gitignore`/`.containerignore` schließen Schlüssel aus | A.8.4, A.8.28 |
| Automatische Tests | `security_test.go`: Köpfe, Host-Prüfung, Begrenzung, Fehlversuche, URL-Prüfung, Bindung der Verschlüsselung | A.8.29 |

## Offen (vor dem Betrieb)

1. **OIDC-Anmeldung** (heute 501): Authorization Code mit PKCE, `state`/`nonce`, feste Redirect-URI aus `ZP_PUBLIC_URL`, Benutzer über (iss, sub), Sitzung als Hash in der DB, Cookie `__Host-` mit Secure/HttpOnly/SameSite, Logout, Sperre wirkt sofort, Geräte-Token nur als Hash mit Ablauf und Widerruf. (A.5.16, A.5.17, A.8.5)
2. **Containerfile und OpenShift-Manifeste:** nonroot, read-only Root-Dateisystem, Secrets nur aus dem Cluster, NetworkPolicy (Egress nur Datenbank, Plane, Jira, authentik), Resource-Limits, Route mit TLS. (A.8.9, A.8.20)
3. **Datenbankrollen:** Migration und Laufzeit mit getrennten Rollen; Laufzeitrolle ohne `TRUNCATE`/`DISABLE TRIGGER` auf Protokolltabellen. (A.8.2, A.8.15)
4. **Abhängigkeiten:** `govulncheck ./...` (Sandbox ohne Netz) und Aktualisierung von `pgx` regelmäßig. (A.8.8)
5. **Schlüsselrotation und Sicherung:** Verfahren für `ZP_SECRET_KEY`-Wechsel; Sicherung der Datenbank mit getrennter Aufbewahrung des Schlüssels. (A.8.24, A.8.13)
6. **Mac-Clients:** siehe Audit-Bericht Client (Code nur aus signiertem Paket, Token im Schlüsselbund, Datenordner nicht in iCloud, ein einziger Server-Starter, SSH-Befehle nicht protokollieren, lokale API nur mit exaktem Origin und Token). (A.8.1, A.8.7, A.5.34)
7. **Datenschutz:** Anwesenheit, Kalendertitel und SSH-Daten nur auf ausdrücklichen Wunsch an den Zentralserver; Abstimmung mit Betriebsrat/Datenschutz. (A.5.34)
