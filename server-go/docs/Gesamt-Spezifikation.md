# Zeitprotokoll Team-Server – Gesamtspezifikation (Stand 05.11.2026)

Zweck: ein Blatt für Prüfung und Nachschärfen. Status: **fertig** (gebaut, geprüft), **teilweise**, **offen** (nicht gebaut), **Entscheidung nötig**. Alles mit „offen“ ist ausdrücklich nicht festgelegt.

## 1. Ziel
Besseres Tempo für Plane: ein zentraler Server (Go, PostgreSQL, OpenShift) führt Zeitdaten zusammen, bucht nach Plane und – solange nötig – nach Jira/Tempo, steuert Freigabe und Monatsabschluss. Clients (Browser, Add-on, Mac-App) sind schlanke Oberflächen. Das Node-System läuft parallel weiter. **Reihenfolge: Server-Kern → Jira/Tempo → Container/OpenShift → erst danach Client.**

**Leitbild (09.11.):** Das Zeitprotokoll löst Tempo ab und dient dabei als Übergangslösung: Zeiten, Freigabe, Teams, Auswertung und Abrechnung führt künftig das Zeitprotokoll; die Buchung nach Jira/Tempo ist nur Brücke, bis alles über Plane läuft, und bleibt abschaltbar. Folge: Tempo-Funktionen, die Beispiel braucht (Kunden-/Account-Auswertung, Berichte, Exporte, Historie per Grundimport), gehören in den Kern; Tempo Cloud ist nachrangig.

**Grundsatz (09.11.):** Das Zeitprotokoll hält die Zeiten selbst und genau (Beginn, Ende, Pausen, Kommentar) – es ist die Quelle der Wahrheit, weil Plane das nicht so genau kann. Plane bekommt nur Summen je Ticket (ein Worklog `[zp-sum]`), Jira/Tempo einzelne Worklogs. Es ist ein „Tempo für Plane“, mit Jira als zweiter Anbindung. Importe füllen nur Lücken: Tempo-Zeiten (haben Beginn) gelten als genau; Plane-Zeiten (nur Dauer je Tag/Ticket) werden als ungenau markiert, überschreiben nie vorhandene Buchungen und übernehmen keine vom Zeitprotokoll selbst gebuchten Summen ([zp-sum]) zurück.

**Priorität (Entscheidung 09.11.):** Zuerst Jira Data Center (mit Tempo Timesheets DC) und Plane (selbst gehostet) – das muss sauber laufen. Danach die Cloud-Anbindung für beide: Jira Cloud (mit Tempo Cloud) und Plane Cloud. Die Datenmodelle (mehrere Jira-Instanzen, Plane-Adresse je Server) bleiben dafür offen gehalten, Cloud wird erst nach Abnahme von Data Center/Plane gebaut.

## 2. Architektur
| Teil | Festlegung | Status |
|---|---|---|
| Backend | Go, eine Programmdatei, `server-go/` | fertig |
| Datenbank | PostgreSQL, Migrationen 001–012 beim Start, `schema_migrations` | fertig |
| Mehrbenutzer | jede Abfrage filtert `user_id`; Zeiten als TEXT im Node-Format, Zeitzone `ZP_TZ` | fertig |
| Oberfläche | Browser-first: Weboberfläche (Vite/React, vorhanden), Admin `/admin`, Login `/login` | teilweise (Admin: kein LDAP/OIDC-Formular) |
| Betrieb | Befehlszeile `server …`, Selbsttest beim Start und alle 6 h, Logs, Audit mit Hash-Kette | fertig |
| Container/OpenShift | Containerfile, Manifeste, getrennte DB-Rollen, `govulncheck` | **offen** |
| Konfiguration | Umgebung (`ZP_*`) plus `server_config` in der DB | fertig |

## 3. Anmeldung und Konten
| Funktion | Beschreibung | Status |
|---|---|---|
| Lokale Konten | Benutzername/Passwort, PBKDF2 600 000, ≥12 Zeichen, drei Zeichenarten, Sitzung 12 h / 2 h Leerlauf | fertig |
| Einzelbetrieb | solange `local`: höchstens 1 produktive Person + 1 Test-Administrator | fertig |
| Notfallzugang | Rolle `breakglass`, nur per Befehl scharf (5–240 min), nie Zeitdaten, nur Betrieb + Monatsabschluss | fertig |
| LDAP/AD | Bind+Suche, LDAPS/StartTLS, Gruppen als voller DN, Nachprüfung alle 30 min (nur mit Dienstkonto) | fertig, nicht gegen echtes Verzeichnis getestet |
| OIDC (authentik, Keycloak, Entra) | PKCE, state/nonce, Rücksprung aus `ZP_PUBLIC_URL`, Kennung (iss,sub), OIDC-Sitzung 8 h / Admin 4 h | fertig, nicht gegen echtes authentik getestet |
| Verknüpfung bestehender Konten | `ldap link` / `oidc link <name> <sub>`, einmalig, 24 h | fertig |
| Web-Maske LDAP/OIDC | Einrichtung in `/admin` statt per Befehl | **offen** |
| Geräte-Token (Mac-App, Add-on, curl) | gehasht, widerrufbar, nie für Admin-Funktionen | **offen** |
| E-Mail-Versand | Wunsch des Nutzers, Verwendungszweck und Technik (SMTP/Exchange-OAuth/Graph) | **offen** |
| Nachprüfung OIDC im IdP | Sperrung wirkt erst bei neuer Anmeldung; Behelf `server user logout` | Entscheidung nötig |

## 4. Rollen und Sichtbarkeit (Entscheidung 05.11.)
Drei Rollen, **kombinierbar** (eine Person kann Teamleiter und Personal zugleich sein):
- **Mitarbeiter:** sieht und bucht nur sich selbst.
- **Teamleiter:** sieht die Buchungen seines Teams (Einzelbuchungen, wie in Tempo).
- **Personal:** sieht alle Personen.
- **Admin (technisch):** Server, Konten, Einstellungen; sieht **keine** Zeitdaten per Rolle – **Entscheidung nötig**, ob der Admin zugleich Personal ist.
- Notfallzugang bleibt außerhalb der Rollen.

Heute im Server: `user | lead | admin` als Einzelwert. **Änderung nötig:** Merkmale statt Einzelrolle (z. B. `is_lead`, `is_hr`, `is_admin`), Teams (Zuordnung Person ↔ Team, Teamleiter je Team). Gruppen im Verzeichnis: Vorschlag `zp-users`, `zp-leads`, `zp-hr`, `zp-admins` – **Entscheidung nötig**. Das Konzept sieht noch vier Rollen (mit „Buchhaltung“) vor und ist anzupassen.

## 5. Zeitdaten (Kern)
| Funktion | Status |
|---|---|
| Zeiteinträge mit Plausibilitätsprüfung, Timer/Pause | fertig |
| Überblick, Konto (Saldo, Soll/Ist), Abwesenheiten, Feiertage | fertig |
| Planung, Berichte, Stundenzettel, Raster | fertig |
| Prüfregeln (`checks`), Hinweise | fertig |
| Monatsabschluss (`closeMonth`), Sperre | fertig |
| Anwesenheit (je Minute „jemand am Rechner“, 120 Tage) | fertig im Server, Quelle ist der Client |
| To-do-Liste | **offen** |
| Assistent (Vorschläge, Einrichtungshilfe) | **offen**, laut Konzept „noch nicht bauen“ |
| Import aus dem lokalen Tracker (SQLite) | **entfällt** (Entscheidung 09.11.) |
| Import von Zeiten aus Tempo (Grundimport) und aus Plane (Worklogs der Person) | **offen** |
| Exporte (Lohn, Kunden; nur freigegebene Monate) | **offen** |

## 6. Freigabe-Workflow
Ziel: gleicher Mechanismus wie heute in Jira/Tempo, nun für Plane (Entscheidung 05.11.). **Genaue Regeln offen**, Fragen:
1. Wer gibt heute frei (Teamleiter, Personal, beide)? Zweistufig?
2. Teams: aus Tempo übernehmen, aus Verzeichnisgruppen, oder in `/admin` pflegen?
3. Fristen/Erinnerungen (Konzept-Vorschlag: einreichen bis 3., freigeben bis 5. Werktag)?
4. Ablehnen nur mit Kommentar; Wiedereröffnen eines freigegebenen Monats – durch wen, mit Protokoll?
5. Nachträge nach Freigabe (Konto, Abwesenheit, Korrektur) – erlaubt, gesperrt, nur Personal?

Bau: Status je Person und Monat (offen → eingereicht → freigegeben/abgelehnt), Sperre der Einträge, Audit. Für Jira/Tempo-Buchungen bleibt der Tempo-Stundenzettel-Status maßgeblich (Spezifikation Jira/Tempo). Status: **offen**.

## 7. Plane (Modul, nicht abschaltbar)
**Mehrere Plane-Workspaces (Entscheidung 10.11.):** Der Administrator legt mehrere Plane-Workspaces fest (Name, Adresse, Workspace-Kennung, ein/aus; änderbar, mehrere Einträge – wie die Jira-Instanzen). Heute gibt es nur einen (`plane_base_url` + `plane_workspace` in der Servereinrichtung). Folgen: Quellen/Projekte, Sync, Tickets, Import und Buchung arbeiten je Workspace; der API-Schlüssel gilt je Person und Plane-Adresse (Workspaces auf demselben Server teilen ihn). Status: **offen, nächster Baustein nach Schritt 2**.

Persönlicher API-Schlüssel je Person (verschlüsselt, an Host gebunden), Adresse/Workspace für alle. Bleibt so (keine Plane-App/OAuth).
| Funktion | Status |
|---|---|
| Buchung: eine Summen-Worklog je Ticket (`[zp-sum]`), kein Duplikat, Entfernen | fertig, Ende-zu-Ende mit PRJA-762 geprüft |
| Tickets suchen, Entwürfe (nur auf ausdrücklichen Push) anlegen | fertig |
| Bremse pro Person (45/58 pro Minute, 429-Behandlung), Schutz vor Umleitung/Metadaten-IPs | fertig |
| Quellen/Projekte (nur aus Plane/Jira vorhanden, Verknüpfung an einer Stelle) | fertig (nur Plane), Jira offen |
| Plane-Erinnerung „0h 0m nach Entfernen“ von PRJA-762 | vom Nutzer zu bestätigen |

## 8. Jira/Tempo (Modul, abschaltbar)
Grundlage: [Jira-Tempo-Spezifikation.md](Jira-Tempo-Spezifikation.md) (Verhalten des Node-Systems).
- **Jira Data Center + Tempo Timesheets DC:** Nachbau von Buchen (ein Eintrag = ein Worklog), Übergangs-Ticket je Person/Monat, Monatssperre aus Stundenzettel-Status, Tempo→lokal alle 12 h, Grundimport. Status: **offen** (Tabellen im Schema vorhanden).
- **Jira Cloud (E-Mail + API-Token) und Tempo Cloud (api.tempo.io):** im Node-System **nicht vorhanden**, Neubau. **offen**.
- Entscheidung nötig: Tempo-Token-Art (Code und Kommentar widersprechen sich); Grenze „Zählung ab 01.10.2026“ ist Einstellung `tempo_push_since`; Nachträge in geschlossene Monate/Monatstickets: **entschieden 09.11.: nur mit Freigabe** – ein eingereichter oder freigegebener Monat bleibt gesperrt (Datenbank-Sperre); Nachträge gehen erst nach Wiedereröffnen durch Personal (mit Begründung, protokolliert), danach wird das Monatsticket wieder geöffnet und nach erneuter Freigabe geschlossen; ob Jira Cloud ohne Tempo (nur Jira-Worklogs) reichen soll.
- Modul abschaltbar; auch „nur Jira, ohne Tempo“ muss gehen.

### Tracken ohne Ticket (Entscheidung 09.11.)
Zeit lässt sich auch ohne Ticket erfassen. Erst beim Übertragen (Plane oder Jira/Tempo) braucht jede Buchung ein Ticket: Der Server macht Vorschläge (passende vorhandene Tickets per Textabgleich) oder legt auf Bestätigung der Person ein neues an (Jira-Ticket im eingestellten Projekt je Instanz, in Plane ein Work Item/Entwurf). Ohne Bestätigung wird nichts gebucht (Status „needs“). Stufe 1 des Tempo-Moduls legt noch keine „NEU:“-Tickets an – offen, nächster Baustein.

Der Ticket-Vorschlag im Server ist die **Light-Variante** (nur Textabgleich). Die bessere Variante mit KI liefert später der Client (Mac); der Server bleibt dafür offen (Client kann Vorschläge liefern, die Person übernimmt sie). Der Vorschlag ist **dreifach abschaltbar**: (1) Admin für alle (`module_suggest`), (2) Admin je Person (`suggest_blocked`), (3) Person für sich (`suggest_enabled`). Abgeschaltet gibt es keine Vorschläge und keine Abfragen dafür; das manuelle Zuordnen eines Tickets bleibt möglich.

### Zugangsdaten je Person
Jede Person trägt ihren Plane-API-Schlüssel und je Jira-Instanz ihr Jira-Token selbst ein (verschlüsselt, nie wieder angezeigt). Kein gemeinsamer Schlüssel, kein eigenes Tempo-Token.

### Vier Oberflächen je Rolle (Entscheidung 10.11.)
Eine Anmeldung, aber getrennte Oberflächen – jede Person sieht nur, was zu ihrer Rolle gehört:
| Oberfläche | Für | Inhalt | Adresse |
|---|---|---|---|
| **Administration** | technischer Admin | Konten, Teams, Einstellungen, Jira-Instanzen, Plane-Workspaces, Logs, Selbsttest, Notfallzugang – **keine Zeiten** | `/admin` (gebaut) |
| **Mitarbeiter** | alle, die Zeiten erfassen | das bisherige Zeitprotokoll: Heute, Woche, To-dos, Kalender, Abwesenheiten, Auswertung, Übertragen, Einstellungen (nur persönliche: Plane-/Jira-Token, Arbeitszeit); Monat einreichen | `/` (alte Oberfläche läuft am Go-Server, Anpassung läuft) |
| **Teamleiter** | Leitung eines Teams | Team-Übersicht (Salden, Einzelbuchungen wie Tempo), Monate des Teams freigeben/ablehnen | `/team` (offen) |
| **Personal** | Personal/GL | alle Personen, alle Monate freigeben/wiedereröffnen, Auswertungen, Exporte | `/personal` (offen) |
Rollen sind kombinierbar: Wer Mitarbeiter und Teamleiter ist, hat beide Bereiche (Umschalter in der Navigation). Die Server-Schnittstellen (Freigabe, Sichtbarkeit `?user=`) gibt es; es fehlen die Bildschirme. Admin-Dinge stehen nie in den anderen Oberflächen (der alte Reiter „Einstellungen › Server“ wandert nach `/admin`).

### Oberfläche
Die Bedienoberfläche (Web, danach Clients) wird erst gebaut, wenn der Server im Hintergrund feststeht, und folgt den Entscheidungen des lokalen Zeittrackers (Node/Mac-App), damit Bedienung und Begriffe gleich bleiben.

## 9. Kalender, Meetings, SSH, Anwesenheit (Module, abschaltbar)
- **Kalender (Exchange/CalDAV) über den Server:** laut Konzept zentral; Verfahren (Graph/EWS/OAuth, Rechte, Speicherung) **offen**, Datenschutzfolgen **offen**.
- **Meetings, SSH-Sitzungen, Anwesenheit:** liefert der Client lokal; der Server nimmt Vorschläge/Anwesenheit entgegen. Vorschläge sind **keine** Buchungen, erst „Buchen“ macht Zeit daraus. Server-Seite: Anwesenheit fertig; Vorschlags-Schnittstelle **offen**.
- Module einzeln abschaltbar (Schalter in `/admin`): **offen**.

### Saubere Trennung Server ↔ Client (Entscheidung 09.11.)
Alles, was lokales Wissen vom Rechner braucht, gehört **nur** in den Client; der Server funktioniert vollständig ohne.
| Fähigkeit | Wo | Anmerkung |
|---|---|---|
| Zeiten, Freigabe, Teams, Auswertung, Plane/Jira/Tempo-Buchung, Textabgleich-Ticketvorschlag (light) | **Server** | läuft im Browser komplett |
| SSH-Erkennung, Meeting-Erkennung, Anwesenheit am Rechner, Safari-Ticket-Erkennung, Tray | **Client** (Minimal- bzw. Voll-Client) | Rohdaten bleiben lokal |
| Ticket-Vorschlag mit KI | **Client** (Mac) | Server nimmt Vorschläge nur entgegen |
| Kalender (Exchange/CalDAV) | Server (zentral) oder Client – **offen** | siehe Abschnitt 9 |
Die Server-Seite kennt für Client-Signale nur eine **allgemeine Annahme-Schnittstelle** (Vorschläge und Anwesenheit mit Art `ssh|meeting|calendar|presence`, nie Rohdaten, nie automatisch gebucht). Jede Art ist einzeln abschaltbar: für alle (Admin), je Person (Admin) und für sich (Person), wie der Ticket-Vorschlag. In der Oberfläche trägt jede Funktion ihre Mindeststufe („braucht Client“); ohne passenden Client wird sie nicht versteckt, sondern mit Hinweis markiert. Im Server-Code gibt es keine SSH-/Meeting-Erkennung.

## 10. Offene API und Plugins
Offene REST-API mit Token, Module/Plugins einzeln abschaltbar (Konzept). Status **offen**; hängt an Geräte-Token (Abschnitt 3). Admin-Funktionen sind nie über Token erreichbar (Festlegung).

## 11. Sicherheit und ISO 27001
Gebaut und zweimal bzw. dreimal von Opus geprüft: Sitzungen (gehasht), CSRF (Origin/Fetch-Metadata), Host-Prüfung, Rate-Limits, Login-Sperren ohne Benutzer-Aufzählung, Secrets mit AAD, Audit mit Hash-Kette und Schutz-Trigger, Notfallzugang nur Betrieb, Pflichtprüfungen in Produktion (Schlüssel ≥32 Zeichen, kein `none`). Doku: `docs/Sicherheit-Server-ISO27001.md`.
Offen: getrennte DB-Rollen, `govulncheck`, Backup/Wiederherstellung und Aufbewahrungsfristen (mit Buchhaltung/Datenschutz), Betriebsvereinbarung, Einwilligung/Transparenz bei Anwesenheit und Kalender, Penetrationstest vor Pilot.

## 12. Clients (erst nach Kern, Jira/Tempo, Container)
| Stufe | Inhalt | Status |
|---|---|---|
| Nur Browser | buchen, Woche, Kalender, To-dos, Auswertung, Freigabe; Timer serverseitig | teilweise (Oberfläche vorhanden, Freigabe/To-dos offen) |
| Browser + Add-on | Ticket mit einem Klick tracken | Server-Kopplung **offen** |
| Minimal-Client | Hintergrunddienst: Anwesenheit, SSH, Leerlauf, Offline-Puffer | **offen** |
| Voll-Client (Mac-App) | Tracker-Fenster, Termin-/Meeting-Erkennung, Tray | läuft heute gegen Node; Umstellung **offen** |
- **Keine Server-Administration im Client.** Administration nur in der Web-Oberfläche des Servers oder per Befehl am Server.
- Härtung Mac-Client (Reihenfolge-Vorschlag): Absturzschleife/SSH-Log, lokale API: Origin- und Einstellungs-Allowlist, Schlüssel in den Schlüsselbund, Datenordner nicht in `~/Documents` (iCloud-Sync-Risiko), Geräte-Token statt Zugangsdaten im Client.
- Windows/Linux: Pilot oder erst Phase 4 – **Entscheidung nötig**. Handy-PWA: später.

## 13. Betrieb und Rollout
Pilot: Vorschlag 3 Personen (Betrieb und Entwicklung), 1 Monat – **Entscheidung nötig**. PostgreSQL auf eigener VM oder vorhandenem DB-Server – **Entscheidung nötig**. Betriebsvereinbarung vor Team-Funktionen. Node-System bleibt bis zur Abnahme parallel. Kein Import aus dem lokalen Tracker; Historie kommt aus Tempo (Grundimport) und Plane (Worklogs).

## 14. Änderungen am Konzeptpapier (Vorschläge)
1. Rollen: vier Rollen → drei kombinierbare (Mitarbeiter, Teamleiter, Personal) plus technischer Admin; „Buchhaltung“ entfällt oder wird Personal.
2. Sichtbarkeit: Teamleiter sehen Einzelbuchungen (wie Tempo); Betriebsrat vorab klären.
3. Plane: persönlicher Token festgeschrieben (Plane-App/OAuth gestrichen).
4. Jira: Cloud und Tempo Cloud zusätzlich zu Data Center aufnehmen; Modul abschaltbar.
5. Anmeldung: lokale Konten, Notfallzugang und Einzelbetrieb als Übergang aufnehmen; Bezug auf LDAP (voller Gruppen-DN) und OIDC-Details.
6. Entscheidungen „Zeit nach Tempo ab 01.10.2026“ als Einstellung `tempo_push_since` benennen.
7. Offene Fragen aus Abschnitt 6 und 13 dort eintragen.

## 15. Nächste Schritte (Vorschlag)
1. Rollen als Merkmale + Teams + Verzeichnisgruppen. 2. Freigabe-Workflow (nach Klärung Abschnitt 6). 3. Web-Maske LDAP/OIDC. 4. Jira/Tempo DC, danach Cloud. 5. Modul-Schalter, Kalender, Vorschlags-Schnittstelle. 6. Container/OpenShift, DB-Rollen, `govulncheck`. 7. Geräte-Token, offene API. 8. Client-Anbindung und Mac-Härtung.

## 16. Entscheidungen zu Freigabe, Teams, Erinnerungen, Urlaub (10.11.2026)
Antworten des Nutzers auf die acht Fragen des Systemvergleichs:
1. **Monatsschluss:** Nach Monatsende kann die Person nur noch bis zum Ende des ersten Werktags danach nachtragen, dann ist der Monat zu (automatisch). Danach nachtragen nur Teamleitung (ihres Teams) und Personal, mit Begründung und Protokoll; der technische Admin sieht und ändert keine Zeiten. – in Arbeit.
2. **Freigabe-Regeln (Fristen, Modus):** Servereinstellungen in `/admin` (Standard: Frist 1 Werktag, Modus „Personal gibt frei“); Fachliches kann später zum Personal wandern – nicht entschieden.
3. **Teams:** aus dem Verzeichnis (LDAP-/authentik-Gruppen `zp-team-<name>`, `-lead`) **oder** lokal im Admin; beides parallel, Verzeichnis-Teams nicht von Hand änderbar. – in Arbeit.
4. **Selbstfreigabe:** Niemand gibt den eigenen Monat frei; Freigaben macht ausschließlich das Personal (gegenseitig). Einzige Person im Personal: Selbstfreigabe mit Pflicht-Kommentar und eigenem Audit.
5. **Vertretung:** Keine eigene Vertretung; Personal gibt frei und übernimmt jede Freigabe. Teamleitung gibt nicht frei.
6. **Erinnerungen:** In-App-Hinweise (gebaut) **und E-Mail, wenn möglich** (Technik offen: SMTP-Relay, Exchange Online per OAuth/XOAUTH2 oder Graph; abschaltbar, minimaler Inhalt ohne Zeiten). – nächster Baustein.
7. **Exporte (Lohn/Kunden, „exportiert“-Sperre):** kommt später, noch nicht entschieden.
8. **Urlaub:** Urlaubsantrag **mit Genehmigung durch die Teamleitung**; Vertretung: Personal (oder Admin). Offen: Admin = Personal oder technischer Admin (Empfehlung: Personal, weil der technische Admin keine Zeitdaten sieht). – nach E-Mail-Baustein.
