# Zeitprotokoll Team-Server – Gesamtspezifikation (Stand 10.11.2026, nachgeführt am Abend)

Zweck: ein Blatt für Prüfung und Nachschärfen; Kurzüberblick zum Stand: `Stand-und-Plan.md`. Status: **fertig** (gebaut, geprüft), **teilweise**, **offen** (nicht gebaut), **Entscheidung nötig**. Alles mit „offen“ ist ausdrücklich nicht festgelegt.

## 1. Ziel
Besseres Tempo für Plane: ein zentraler Server (Go, PostgreSQL, OpenShift) führt Zeitdaten zusammen, bucht nach Plane und – solange nötig – nach Jira/Tempo, steuert Freigabe und Monatsabschluss. Clients (Browser, Add-on, Mac-App) sind schlanke Oberflächen. Das Node-System läuft parallel weiter. **Reihenfolge: Server-Kern → Jira/Tempo → Container/OpenShift → erst danach Client.**

**Leitbild (09.11.):** Das Zeitprotokoll löst Tempo ab und dient dabei als Übergangslösung: Zeiten, Freigabe, Teams, Auswertung und Abrechnung führt künftig das Zeitprotokoll; die Buchung nach Jira/Tempo ist nur Brücke, bis alles über Plane läuft, und bleibt abschaltbar. Folge: Tempo-Funktionen, die Beispiel braucht (Kunden-/Account-Auswertung, Berichte, Exporte, Historie per Grundimport), gehören in den Kern; Tempo Cloud ist nachrangig.

**Grundsatz (09.11.):** Das Zeitprotokoll hält die Zeiten selbst und genau (Beginn, Ende, Pausen, Kommentar) – es ist die Quelle der Wahrheit, weil Plane das nicht so genau kann. Plane bekommt nur Summen je Ticket (ein Worklog `[zp-sum]`), Jira/Tempo einzelne Worklogs. Es ist ein „Tempo für Plane“, mit Jira als zweiter Anbindung. Importe füllen nur Lücken: Tempo-Zeiten (haben Beginn) gelten als genau; Plane-Zeiten (nur Dauer je Tag/Ticket) werden als ungenau markiert, überschreiben nie vorhandene Buchungen und übernehmen keine vom Zeitprotokoll selbst gebuchten Summen ([zp-sum]) zurück.

**Priorität (Entscheidung 09.11.):** Zuerst Jira Data Center (mit Tempo Timesheets DC) und Plane (selbst gehostet) – das muss sauber laufen. Danach die Cloud-Anbindung für beide: Jira Cloud (mit Tempo Cloud) und Plane Cloud. Die Datenmodelle (mehrere Jira-Instanzen, Plane-Adresse je Server) bleiben dafür offen gehalten, Cloud wird erst nach Abnahme von Data Center/Plane gebaut.

## 2. Architektur
| Teil | Festlegung | Status |
|---|---|---|
| Backend | Go, eine Programmdatei, `server-go/` | fertig |
| Datenbank | PostgreSQL, Migrationen 001–017 beim Start (018 in Arbeit), `schema_migrations` | fertig |
| Mehrbenutzer | jede Abfrage filtert `user_id`; Zeiten als TEXT im Node-Format, Zeitzone `ZP_TZ` | fertig |
| Oberfläche | Browser-first: alte React-Oberfläche läuft im „Servermodus“ am Go-Server (`/`), Administration `/admin`, Anmeldung `/login`, Profil `/profile` | Mitarbeiter-Bereich gebaut (im Browser noch nicht abgenommen); Teamleiter/Personal-Bildschirme **offen**; Admin: kein LDAP/OIDC-Formular |
| Betrieb | Befehlszeile `server …`, Selbsttest beim Start und alle 6 h, Logs, Audit mit Hash-Kette | fertig |
| Container/OpenShift/Linux | Containerfile, OpenShift-Manifeste, Linux-Paket mit systemd-Installer (`scripts/package.sh`), lokaler Test `scripts/dev.sh` | gebaut, noch nicht auf OpenShift/Linux gelaufen; DB-Rollen-Skript + `server rekey` da; `govulncheck` bei Releases |
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
| Geräte-Token (Mac-App, Add-on, curl) | gehasht, widerrufbar, nie für Admin-Funktionen; Annahme-Schnittstelle `client_hints` | gebaut (Migration 021), Browser-/Client-Abnahme offen |
| Eigenes Profil (Plane-Schlüssel, Jira-Token je Instanz, Vorschläge, Passwort) | in der Oberfläche (Servermodus) und als einfache Seite `/profile`; Administration kann Zugangsdaten löschen, nie lesen | fertig |
| E-Mail-Versand | gewünscht (Erinnerungen); Technik SMTP-Relay/Exchange-OAuth/Graph | **offen**, nächster Baustein |
| Nachprüfung OIDC im IdP | Sperrung wirkt erst bei neuer Anmeldung; Behelf `server user logout` | Entscheidung nötig |

## 4. Rollen und Sichtbarkeit (Entscheidung 05.11., nachgeschärft 10.11./11.11.)
Drei Fachrollen, **untereinander kombinierbar** (eine Person kann Teamleiter und Personal zugleich sein):
- **Mitarbeiter:** sieht und bucht nur sich selbst.
- **Teamleiter:** sieht die Buchungen seines Teams (Einzelbuchungen, wie in Tempo).
- **Personal:** sieht alle Personen.
- **Admin (technisch):** getrenntes Konto – Server, Konten, Einstellungen; **nie** Zeiterfassung, **keine** Doppelrolle mit Mitarbeiter/Teamleiter/Personal (Entscheidung 10.11./11.11.). LDAP/OIDC: eigene Admin-Gruppe; dieselbe Person nicht zugleich in Mitarbeiter- und Admin-Gruppe.
- Notfallzugang bleibt außerhalb der Rollen.

**Umgesetzt:** Merkmale statt Einzelrolle (`users.is_hr`, Teamleitung je Team in `team_members.is_lead`), Teams (lokal im Admin, aus Verzeichnisgruppen), Gruppe `group_hr` für Personal (LDAP/OIDC), Sichtbarkeit über `?user=`-Lesezugriff nur nach Rolle. `role=admin` → `timeAccount()=false`, `productive=false`, Weiterleitung nur nach `/admin`; Mitarbeiter-Oberfläche `/` im Servermodus wie das lokale Zeitprotokoll. „Buchhaltung“ entfällt (Personal).

## 5. Zeitdaten (Kern)
| Funktion | Status |
|---|---|
| Zeiteinträge mit Plausibilitätsprüfung, Timer/Pause | fertig |
| Überblick, Konto (Saldo, Soll/Ist), Abwesenheiten, Feiertage | fertig |
| Planung, Berichte, Stundenzettel, Raster | fertig |
| Prüfregeln (`checks`), Hinweise | fertig |
| Monatsabschluss (`closeMonth`), Sperre | fertig |
| Anwesenheit (je Minute „jemand am Rechner“, 120 Tage) | fertig im Server, Quelle ist der Client |
| To-do-Liste, Arbeitstag, Monatsende („Zeiten nachtragen“), Lücken-/Jetzt-vermutlich-Vorschläge, Ticket-Schätzungen, Abwesenheits-Hinweise | fertig (Light-Variante, regelbasiert, vierstufig abschaltbar) |
| Assistent | fertig (regelbasiert, ohne KI); Schritte mit lokalem Wissen bleiben im Client |
| Import aus dem lokalen Tracker (SQLite) | **entfällt** (Entscheidung 09.11.) |
| Import von Zeiten aus Plane (Worklogs der Person, ungenau markiert, auf Anforderung) | fertig |
| Grundimport/Abgleich aus Tempo | gebaut (`/api/tempo-sync`), Abnahme gegen echtes Tempo offen |
| Exporte (Lohn, Kunden; nur freigegebene Monate) | **offen** |

## 6. Freigabe-Workflow
Stand 10.11.2026 (Entscheidungen §16). Rollenklarstellung: **Technischer Admin = nur Technik, keine Zeitdaten.** „Admin“ im Sinne von Nachtragen und Freigeben ist das **Personal** (`users.is_hr`). Notfallzugang nie.

**Freigabe (optionaler früherer Abschluss/Prüfschritt, unverändert):** Person reicht ein → Personal gibt frei oder lehnt mit Kommentar ab; Personal öffnet wieder (Begründung, Protokoll). Standard `approval_mode = hr` (nur Personal; `either`/`lead` bleiben einstellbar). Teamleitung gibt nicht frei, keine Vertretung – Personal übernimmt jede Freigabe. Niemand gibt den eigenen Monat frei; Ausnahme: Gibt es genau eine aktive, zeiterfassende Person im Personal, darf sie ihren Monat selbst freigeben (Pflicht-Kommentar ≥ 10 Zeichen, Verlauf „Selbstfreigabe: einzige Person im Personal“, Audit `approval.self_approve`).

**Regel 1 – Monatsschluss mit Nachfrist (automatisch, `monthclose.go`, Migration 018):** Nach Monatsende trägt die Person nur noch bis zum Ende des n-ten Werktags danach nach (Mo–Fr ohne Feiertage der serverweiten Liste – eigene „feiertag“-Abwesenheiten zählen nicht, die Person könnte sonst die Frist verlängern; `month_grace_workdays`, Standard 1, 0–5, `/admin` › Monatsfreigabe). Danach Status `closed` („automatisch geschlossen“, Verlauf `auto_close`, Audit `approval.auto_close`). Lauf beim Start und alle 15 min, eine Instanz zugleich (Advisory-Sperre 727005), idempotent; betrifft zeiterfassende, nicht gesperrte Konten, Monate ab Erfassungsbeginn (früheste Buchung bzw. Kontoanlage, nicht die selbst änderbare Einstellung), höchstens die letzten 3. Monate mit Status bleiben (abgelehnt/wiedereröffnet = zur Korrektur zurückgegeben: wird nicht automatisch geschlossen). `closed` sperrt wie `approved` (Datenbank-Sperre, Übergangs-Ticket). Zurückziehen stellt den Stand vor dem Einreichen her: nach Ablehnung/Wiedereröffnen `rejected`, sonst nach Fristablauf `closed`, davor offen. Laufender Timer aus dem Monat: Schließen wartet bis 14 Tage nach Fristende (Hinweis an die Person); danach wird geschlossen, ein späteres Stoppen beendet den Timer und speichert nur den Teil im offenen Monat (Rest im Systemlog, Nachtrag durch Teamleitung/Personal); rückwirkender Timerstart in einen gesperrten Monat beginnt am Monatsersten. Hinweise: „Monat X schließt am …“, „Monat X ist geschlossen“.

**Regel 2 – Nachtragen nach Monatsschluss (`nachtrag.go`):** In geschlossenen, eingereichten oder freigegebenen Monaten schreibt die Person nichts mehr (409 `month_locked`). Nachtragen dürfen die Teamleitung der Person (Leitung im selben Team) und das Personal – nicht der technische Admin über seine Rolle, nicht der Notfallzugang, nie für den eigenen Monat. Routen `POST/PUT/DELETE /api/team/{userId}/time-entries[/{id}]` und `…/absences[/{id}]`, Pflichtfeld `reason` (10–500 Zeichen), dieselben Prüfungen wie die normalen Schreibwege; offener Monat → 400 „Monat ist offen – bitte normal eintragen“. Die Sperre fällt nur in der Schreib-Transaktion und nur für die Zielperson (`set_config('zp.bypass_user'/'zp.actor', …, true)` nach bestandener Prüfung). Protokoll: `entry_audit` (actor, origin `nachtrag`, Hash-Kette unverändert), `audit_log` `nachtrag`, Verlauf `nachtrag`, `time_entries.entered_by` (+ `entered_by_name` in den GET-Antworten), Hinweis an die Person. Nachgetragene Zeiten gehen wie normale nach Plane/Jira (Abgleichsfelder sind nicht gesperrt). Lesen für die Oberfläche: `GET /api/time-entries|absences|projects|tasks?user=<id>` (Sichtbarkeit, protokolliert).

**Regel 3 – Teams aus dem Verzeichnis oder lokal (`teamsync.go`):** `teams.source`/`team_members.source` = `local` | `directory`. Gruppe `<präfix><name>` = Mitglied, `<präfix><name>-lead` = Teamleitung (`ldap_team_prefix`/`oidc_team_prefix`, Standard `zp-team-`, `-` = aus; exakter Vergleich, nur direkte Gruppen; `ldap_team_base` beschränkt auf eine OU – empfohlen, da Teamleitung Einsicht und Nachtragsrecht gibt). OIDC gleicht nur beim Login ab. Abgleich beim Login (LDAP, OIDC) und in der LDAP-Nachprüfung; fehlende Teams entstehen als `directory`, nur `directory`-Mitgliedschaften werden gesetzt/entfernt. Ein gleichnamiges lokales Team wird nicht übernommen (Systemlog). Verzeichnis-Teams: Mitglieder/Leitung in `/admin` und per Befehl nicht änderbar (409), löschen nur leer.

**Benachrichtigungen:** In-App-Hinweise und E-Mail über `notifyApproval` (SMTP, abschaltbar in `/admin`, Migration 020; Ereignisse closing_soon, closed, rejected, nachtrag, pending; Inhalt ohne Zeiten; Entprellung in `notify_sent`). Exchange-OAuth/Graph offen. Für Jira/Tempo-Buchungen bleibt der Tempo-Stundenzettel-Status maßgeblich.

## 7. Plane (Modul, nicht abschaltbar)
**Mehrere Plane-Workspaces (Entscheidung 10.11.):** Der Administrator legt mehrere Plane-Workspaces fest (Name, Adresse, Workspace-Kennung aus der Plane-URL, ein/aus; änderbar, mehrere Einträge – wie die Jira-Instanzen). **Die Administration braucht keinen Plane-API-Schlüssel** und ruft Plane nicht auf; sie kennt nur die Organisations-Adressen. Migration 023/024/025 + Admin `/admin`, Nutzer `/profile` (Schlüssel je Host). Mit persönlichem Token wählt die Person selbst, welche Workspaces sie nutzt (`user_plane_workspaces`, `PUT /api/plane/workspaces/{id}/select`). Quellen, Tickets, Import, Push und Status über die gewählten Workspaces (`?workspace=` oder aus Projekt/Ticket); FK `plane_workspace_id`; Primär-Workspace spiegelt `plane_base_url`/`plane_workspace` für Tempo-Links. Status: **gebaut, Abnahme offen**.

Persönlicher API-Schlüssel je Person (verschlüsselt, an Host gebunden), Adresse/Workspace-Kennung für alle vom Admin. Bleibt so (keine Plane-App/OAuth, kein gemeinsamer Admin-Token).
| Funktion | Status |
|---|---|
| Buchung: eine Summen-Worklog je Ticket (`[zp-sum]`), kein Duplikat, Entfernen | fertig, Ende-zu-Ende mit PRJA-762 geprüft |
| Tickets suchen, Entwürfe (nur auf ausdrücklichen Push) anlegen | fertig |
| Bremse pro Person (45/58 pro Minute, 429-Behandlung), Schutz vor Umleitung/Metadaten-IPs | fertig |
| Quellen/Projekte (nur aus Plane/Jira vorhanden, Verknüpfung an einer Stelle) | fertig (Plane); Jira-Verknüpfung je Instanz gebaut |
| Import von Plane-Zeiten, Zuordnung Jira→Plane (`map/suggest|apply`), meine Work-Items | fertig (ohne echte Plane-Antworten geprüft) |
| Plane-Erinnerung „0h 0m nach Entfernen“ von PRJA-762 | vom Nutzer zu bestätigen |

## 8. Jira/Tempo (Modul, abschaltbar)
Grundlage: [Jira-Tempo-Spezifikation.md](Jira-Tempo-Spezifikation.md) (Verhalten des Node-Systems).
- **Jira Data Center + Tempo Timesheets DC:** **gebaut:** mehrere Instanzen (Admin legt fest, Token je Person und Instanz), Push (ein Eintrag = ein Worklog) mit Schutz gegen Doppelbuchung, Monatssperre aus Stundenzettel-Status, Ticket-Vorschlag (Light), bestätigtes Anlegen von Tickets, Übergangs-Ticket je Person/Instanz/Monat, Jira-Ticket-Ansicht, Statuswechsel, Kommentar, Tempo-Einstellungen je Instanz, Tempo→lokal-Abgleich und Grundimport (`/api/tempo-sync`). Nie gegen ein echtes Jira/Tempo getestet.
- **Jira Cloud (E-Mail + API-Token) und Tempo Cloud (api.tempo.io):** im Node-System **nicht vorhanden**, Neubau, nachrangig. Cloud-Instanzen lassen sich anlegen, bleiben aber abgeschaltet. **offen**.
- **Entschieden:** Es gibt nur das Jira-Token je Person und Instanz, kein eigenes Tempo-Token. Grenze „Zählung ab 01.10.2026“ ist Einstellung `tempo_push_since`. Nachträge in geschlossene Monate/Monatstickets nur mit Freigabe bzw. durch Teamleitung/Personal nach Monatsschluss (Abschnitt 16); das Monatsticket wird beim Wiedereröffnen geöffnet und nach erneutem Abschluss geschlossen. Offen: ob Jira Cloud ohne Tempo (nur Jira-Worklogs) reichen soll.
- Modul abschaltbar; auch „nur Jira, ohne Tempo“ muss gehen.

### Tracken ohne Ticket (Entscheidung 09.11.)
Zeit lässt sich auch ohne Ticket erfassen. Erst beim Übertragen (Plane oder Jira/Tempo) braucht jede Buchung ein Ticket: Der Server macht Vorschläge (passende vorhandene Tickets per Textabgleich) oder legt auf Bestätigung der Person ein neues an (Jira-Ticket im eingestellten Projekt je Instanz, in Plane ein Work Item/Entwurf). Ohne Bestätigung wird nichts gebucht (Status „needs“). Gebaut: Vorschläge und bestätigtes Anlegen von Jira-Tickets (Idempotenz je Auftrag), Plane-Work-Items über die vorhandenen Plane-Routen.

Der Ticket-Vorschlag im Server ist die **Light-Variante** (nur Textabgleich). Die bessere Variante mit KI liefert später der Client (Mac); der Server bleibt dafür offen (Client kann Vorschläge liefern, die Person übernimmt sie). Der Vorschlag ist **dreifach abschaltbar**: (1) Admin für alle (`module_suggest`), (2) Admin je Person (`suggest_blocked`), (3) Person für sich (`suggest_enabled`). Abgeschaltet gibt es keine Vorschläge und keine Abfragen dafür; das manuelle Zuordnen eines Tickets bleibt möglich.

### Zugangsdaten je Person
Jede Person trägt ihren Plane-API-Schlüssel und je Jira-Instanz ihr Jira-Token selbst ein (verschlüsselt, nie wieder angezeigt). Kein gemeinsamer Schlüssel, kein Admin-Plane-Token, kein eigenes Tempo-Token. Der technische Administrator richtet nur Adressen/Kennungen ein und kann fremde Tokens löschen (nie lesen).

### Vier Oberflächen je Rolle (Entscheidung 10.11.)
Eine Anmeldung, aber getrennte Oberflächen – jede Person sieht nur, was zu ihrer Rolle gehört:
| Oberfläche | Für | Inhalt | Adresse |
|---|---|---|---|
| **Administration** | technischer Admin | Konten, Teams, Einstellungen, Jira-Instanzen, Plane-Workspaces, Logs, Selbsttest, Notfallzugang – **keine Zeiten** | `/admin` (gebaut) |
| **Mitarbeiter** | alle, die Zeiten erfassen | das bisherige Zeitprotokoll: Heute, Woche, To-dos, Kalender, Abwesenheiten, Auswertung, Übertragen, Einstellungen (nur persönliche: Plane-/Jira-Token, Arbeitszeit); Monat einreichen | `/` (alte Oberfläche läuft am Go-Server im Servermodus; im Browser noch nicht abgenommen) |
| **Teamleiter** | Leitung eines Teams | Team-Übersicht (Salden, Einzelbuchungen wie Tempo), Monate des Teams freigeben/ablehnen | `/team` (Server-Übersichten in Arbeit, Bildschirm offen) |
| **Personal** | Personal/GL | alle Personen, alle Monate freigeben/wiedereröffnen, Auswertungen, Exporte | `/personal` (Server-Übersichten in Arbeit, Bildschirm offen) |
Fachrollen sind kombinierbar: Wer Mitarbeiter und Teamleiter ist, hat beide Bereiche (Umschalter in der Navigation). Der technische Admin bleibt ein getrenntes Konto und sieht die Mitarbeiter-GUI nicht. Die Server-Schnittstellen (Freigabe, Sichtbarkeit `?user=`) gibt es; Teamleiter-/Personal-Bildschirme fehlen noch. Admin-Dinge stehen nie in den anderen Oberflächen (der alte Reiter „Einstellungen › Server“ wandert nach `/admin`).

### Oberfläche
Die Bedienoberfläche (Web, danach Clients) wird erst gebaut, wenn der Server im Hintergrund feststeht, und folgt den Entscheidungen des lokalen Zeittrackers (Node/Mac-App), damit Bedienung und Begriffe gleich bleiben.

## 9. Kalender, Meetings, SSH, Anwesenheit (Module, abschaltbar)
- **Kalender (Exchange/CalDAV) über den Server:** laut Konzept zentral; Verfahren (Graph/EWS/OAuth, Rechte, Speicherung) **offen**, Datenschutzfolgen **offen**.
- **Meetings, SSH-Sitzungen, Anwesenheit:** liefert der Client lokal; der Server nimmt Vorschläge/Anwesenheit entgegen. Vorschläge sind **keine** Buchungen, erst „Buchen“ macht Zeit daraus. Server-Seite: Anwesenheit fertig; Vorschlags-Schnittstelle **offen**.
- Module einzeln abschaltbar: Jira/Tempo (`module_tempo`) und Vorschläge (`module_suggest`) gebaut; Kalender, Meetings/SSH/Anwesenheit **offen**.

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
Offen: `govulncheck` regelmäßig, Backup/Wiederherstellung und Aufbewahrungsfristen (mit Buchhaltung/Datenschutz), Betriebsvereinbarung, Einwilligung/Transparenz bei Anwesenheit und Kalender, Penetrationstest vor Pilot. DB-Rollen-Skript und `server rekey` sind vorbereitet.

## 12. Clients (erst nach Kern, Jira/Tempo, Container)
| Stufe | Inhalt | Status |
|---|---|---|
| Nur Browser | buchen, Woche, Kalender, To-dos, Auswertung, Freigabe; Timer serverseitig | Mitarbeiter-Bereich läuft am Go-Server (Servermodus); Team/Personal und Abnahme im Browser offen |
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

## 15. Nächste Schritte (aktuell)
1. Abnahme Browser + echte Systeme. 2. Exchange-OAuth/Graph + Multi-Mail. 3. Exporte. 4. Client-Anbindung und Mac-Härtung. 5. Cloud-Varianten (nachrangig). Plane-Workspaces, LDAP/OIDC-Webmaske, DB-Rollen-Skript und `server rekey` sind gebaut (Abnahme offen).

## 16. Entscheidungen zu Freigabe, Teams, Erinnerungen, Urlaub (10.11.2026)
Antworten des Nutzers auf die acht Fragen des Systemvergleichs:
1. **Monatsschluss:** Nach Monatsende kann die Person nur noch bis zum Ende des ersten Werktags danach nachtragen, dann ist der Monat zu (automatisch). Danach nachtragen nur Teamleitung (ihres Teams) und Personal, mit Begründung und Protokoll; der technische Admin sieht und ändert keine Zeiten. – Server und Oberfläche (Nachtragen-Dialog, „geschlossen“) gebaut (§6), Browser-Abnahme offen.
2. **Freigabe-Regeln (Fristen, Modus):** Servereinstellungen in `/admin` (Standard: Frist 1 Werktag, Modus „Personal gibt frei“); Fachliches kann später zum Personal wandern – nicht entschieden.
3. **Teams:** aus dem Verzeichnis (LDAP-/authentik-Gruppen `zp-team-<name>`, `-lead`) **oder** lokal im Admin; beides parallel, Verzeichnis-Teams nicht von Hand änderbar. – im Server gebaut (§6).
4. **Selbstfreigabe:** Niemand gibt den eigenen Monat frei; Freigaben macht ausschließlich das Personal (gegenseitig). Einzige Person im Personal: Selbstfreigabe mit Pflicht-Kommentar und eigenem Audit.
5. **Vertretung:** Keine eigene Vertretung; Personal gibt frei und übernimmt jede Freigabe. Teamleitung gibt nicht frei.
6. **Erinnerungen:** In-App-Hinweise und SMTP-E-Mail (gebaut, abschaltbar, ohne Zeiten). Exchange Online per OAuth/XOAUTH2 oder Graph – offen.
7. **Exporte (Lohn/Kunden, „exportiert“-Sperre):** kommt später, noch nicht entschieden.
8. **Urlaub:** Urlaubsantrag **mit Genehmigung durch die Teamleitung**; Vertretung: Personal (technischer Admin entscheidet nicht). Gebaut: `/api/leave*`, Genehmigung legt Abwesenheit `urlaub` an; Browser-Abnahme offen.
