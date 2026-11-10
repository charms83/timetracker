# Jira/Tempo-Anbindung – Spezifikation (Ist-Stand Node-Backend, Vorlage für Go)

Quellen (alle in `backend/`): `jira.js` (J), `tempo-push.js` (P), `tempo-sync.js` (S), `server.js` (SV), `sources.js` (SO), `plane.js` (PL). Stand: gelesen am 2026-11-05. Nur Belegtes; Unklares ist markiert.

## 1. Varianten, Authentifizierung, Endpunkte

- Unterstützt wird **nur Jira Data Center/Server + Tempo Timesheets für Jira DC** (REST `tempo-timesheets/4`). Keine Jira Cloud, keine Tempo-Cloud-API (api.tempo.io): P:1-13, SV:1133-1139. Cloud-Varianten: nicht vorhanden.
- Basis-URL: Einstellung `jira_base_url`, Schrägstriche am Ende entfernt (P:62). http oder https; bei https Zertifikatsprüfung mit Firmen-CA aus dem System (`secure.requestOptions`, `withChainRepair`, P:99/122).
- Auth: ausschließlich `Authorization: Bearer <jira_token>` (Personal Access Token von Jira DC), `Accept: application/json`, bei Body `Content-Type: application/json` (P:97-98). Derselbe Token für Jira-REST und Tempo (P:12). Kommentar SV:1136 behauptet „Tempo-eigener Token, nicht Jira-PAT“; SV:1240 verlangt dagegen einen Jira-PAT. Widerspruch im Kommentar, der Code nutzt überall `jira_token` (unklar, welcher Token real eingesetzt wird; P:12 sagt derselbe).
- Timeout 20 s je Anfrage (P:99/114). Kein Basic-Auth, keine Cookies.
- Benutzer: `GET /rest/api/2/myself` liefert `name` (Login) und `key` (z. B. JIRAUSER12345); gespeichert als `jira_username`/`jira_userkey` (P:366-374). Tempo erwartet den **Key** als `worker` (P:65, P:673).

Aufgerufene Endpunkte:
| Zweck | Aufruf | Stelle |
|---|---|---|
| Benutzer | GET `/rest/api/2/myself` | P:372, P:742 |
| Projekte | GET `/rest/api/2/project`, `/project/{key}` | J:108, P:584, P:384 |
| Suche | GET `/rest/api/2/search?startAt&maxResults&fields&jql` (Pagination 100er-Schritte bis 1000) | J:55, P:587-591 |
| Ticket | GET `/rest/api/2/issue/{key}?fields=…`, POST `/issue` (anlegen), PUT `/issue/{key}` (description), POST `/issue/{key}/comment` | J:62-78, P:407, P:519 |
| Statuswechsel | GET/POST `/rest/api/2/issue/{key}/transitions` | J:66/84, P:537-540 |
| Anlege-Metadaten | GET `/rest/api/2/issue/createmeta/{proj}/issuetypes` | P:779 |
| Worklogs lesen | POST `/rest/tempo-timesheets/4/worklogs/search` Body `{from,to,worker:[key]}` | P:248, S:118 |
| Worklog anlegen | POST `/rest/tempo-timesheets/4/worklogs/` (Slash am Ende) | P:687 |
| Worklog ändern/löschen | PUT/DELETE `/rest/tempo-timesheets/4/worklogs/{id}` | P:678-682, P:637 |
| Stundenzettel-Freigabe | GET `/rest/tempo-timesheets/4/timesheet-approval/current?periodStartDate=YYYY-MM-01&userKey=…` | P:242 |
| Tempo-Accounts | GET `/rest/tempo-accounts/1/account`, `/account/{id}`, `/account/key/{key}`, `/link/project/{projId}` | P:381-391, P:793 |
| Work-Attribut | GET `/rest/tempo-core/1/work-attribute` (Account-Attribut-ID) | P:787 |
| Alt-Import (Altweg) | GET/POST `/rest/tempo-timesheets/4/worklogs/search?from&to&limit=500&offset` (Body `{}` = POST), Filter lokal auf Autor | SV:1324, SV:1331-1336 |

Beim Verbindungstest (SV:1243-1259) wird `worklogs/search?from=heute&to=heute&limit=1` geprüft; 401 = „Tempo-Lizenz prüfen“. Hinweise zu Fehlern: 401 „Token prüfen“, 403 „keine Berechtigung (Tempo-Rolle, gesperrter Zeitraum?)“, 404 (P:107-108).
Fehlerklassifikation: Pfad beginnt mit `/rest/api/` → „Jira“, sonst „Tempo“ (P:109).

## 2. Buchen / Abgleich (Ausgang, `tempo-push.js`)

**Grundregel:** Jede abgeschlossene lokale Buchung (`end_time` gesetzt, Dauer > 0, `source` nicht `tempo`/`tempo_csv`, `date >= tempo_push_since`) = genau **ein** Tempo-Worklog (P:229-232). Tempo-Spiegel (`source tempo`) werden nie zurückgeschrieben (P:18).

**Worklog-Body** (P:673-674):
```
{ worker: <jira_userkey>, originTaskId: <Ticket-Key>, started: "YYYY-MM-DDTHH:MM:SS.000" (lokale Zeit, kein Offset, P:137),
  timeSpentSeconds: max(60, auf Minute gerundet), comment: <≤900 Zeichen>,
  attributes: { _Account_: { name:"Account", workAttributeId: <tempo_account_attr_id, Default 2>, value: <Account-Key> } }  // nur wenn Account bestimmt }
```
Dauer: `max(60, round(sek/60)*60)` (P:201). Antwort-ID: `tempoWorklogId ?? id ?? worklogId`, auch Array-Erstelement (P:233).

**Kommentar** (P:138-146, 194-205): `describe` = `[TICKET] Titel – Kommentar` (Kopf `[Projektname]` ohne Ticket; Titel entfällt, wenn gleich Key; Whitespace verdichtet; leer = „Arbeit“; ≤900). Je nach Ziel:
- `month`/`create-month`: `describe · <Projektname> · Plane: <planeUrl>/browse/<KEY>/` (monthComment).
- `collector`: `describe`.
- `ticket` (eigenes Jira-Ticket): nur Kommentar, sonst Titel, sonst „Arbeit“.

**Zielticket (`targetOf`, P:149-187), Reihenfolge:**
1. `time_entries.tempo_target` (von Hand/Vorschlag), falls Key-Format → `ticket`.
2. Task ohne Plane-Work-Item (eigenes Jira-Ticket): `jira_link` > `jira_origin` > `jira_ticket`, wenn `ticket_system='jira'` oder Projektkürzel in `jira_project_keys` → `ticket` (nie Sammel-/Übergangsticket, auch nicht bei Plane-verknüpftem Projekt, v6.27.1).
3. Plane-Buchung und `tempo_plane_mode='month'` (Default): Übergangs-Ticket des Monats (siehe 3); war der Worklog früher auf anderem Ticket (`tempo_push_target`), bleibt er dort.
4. Kein Task → `fallback`. `jira_link`/`jira_origin` als Ticket; Plane-Work-Item: Zuordnung `ticket_map` (status `mapped`, `plane_key`→`jira_key`), sonst `NEU:<jira_new_project>` (Jira-Gegenstück je Plane-Ticket anlegen, Modus `ticket`).
5. Sonst Ticket-Key mit Jira-Kürzel → `ticket`.
6. `fallback`: **Regel 29.10.2026: keine Jira-Buchung ohne Ticket.** Nur Plane-Buchungen dürfen aufs Sammelticket (Projekt `tempo_issue` oder globales `tempo_booking_issue`) bzw. neues Sammelticket (`NEU:`); alles andere = `no-ticket` (Status `needs`, Nutzer muss Ticket wählen).
`isPlaneBooking`: Task mit `plane_work_item_id` oder Projekt, für das `allowsPlane` gilt (P:188-191).

**Konto/Account (P:206-210):** Reihenfolge Buchung (`time_entries.tempo_account`) > Projekt (`projects.tempo_account`) > bei echtem Jira-Ticket leer (Tempo übernimmt den Ticket-Account selbst) > Standard `tempo_account`. Account ist bei Beispiel Pflichtattribut. Fehlt er und Tempo antwortet 400 mit „account“ im Text, wird der Standard-Account des Jira-Projekts ermittelt: `project/{key}` → `tempo-accounts/1/link/project/{id}` (defaultAccount bzw. erster) → `account/{id}`.key; Fallback eingestellter Account (P:380-401, P:689-695). Ohne Ergebnis Fehlermeldung „Automatisch einrichten“.

**Änderungs-/Duplikat-/Löschlogik:**
- Hash `sha1([target, started, secs, comment, account])[:16]` (P:211). Account im Hash nur, wenn gesetzt, bei eigenem Ticket der Standard-Account (Rückwärtskompatibilität, „keine Massen-Geändert“).
- Zuordnung lokal ↔ Worklog: `time_entries.tempo_push_id` (TEXT), `tempo_push_hash`, `tempo_push_target`, `tempo_push_synced_at`, `tempo_push_error` (P:29-32, P:698).
- Status (`statusOf`, P:217-228) in dieser Prüfreihenfolge: `skipped` (tempo_skip) > `norule` (noch nicht gepusht und `allowsTempo(project)` false) > `review` (check_flags ohne check_note, nur ungepusht) > `synced` (ID und Hash gleich) > `locked` (Monat gesperrt oder Fehlertext passt LOCK_RE mit HTTP 4xx) > `dup` (ungepusht, `tempo_dup` gesetzt und ≠ 'ok') > `needs` (kein Ziel) > `new`/`error` > `changed`.
- Push-Ablauf (P:614-718): Muss `tempo_push_enabled` und `worker` haben; Mutex `pushing`. (0) Entwurfs-Tickets in Plane anlegen (`linkTaskToPlane`). (1) vorgemerkte Löschungen aus `tempo_push_deletions`: DELETE, 404 gilt als erledigt; Fehler bleibt vorgemerkt; Abbruch bei Netz(0)/401/403 ohne Sperrtext. (2) Monate prüfen (`checkMonth`, höchstens alle 30 min je Monat), dann je Eintrag: Ticket/Sammelticket/Übergangsticket anlegen falls nötig, dann **ID vorhanden**: Ziel gewechselt (`tempo_push_target` ≠ Ziel; ohne gespeichertes Ziel gilt globales Sammelticket als vorheriges) → alter Worklog DELETE (404 ok) + neu POST; sonst PUT; PUT 404 (in Tempo gelöscht) → neu POST. Ohne ID: POST.
- Löschen lokal: `beforeDelete` (P:728) legt `tempo_push_deletions` an; ebenso Zusammenführen (SV:711) und „Ausnehmen“ (`skip`, P:901-913: Löschung vormerken, Push-Felder leeren, `tempo_skip=1`).
- Duplikaterkennung gegen vorhandene Tempo-Worklogs (`checkMonth`, P:237-262): Worklogs des Monats via `worklogs/search`, eigene IDs (`tempo_push_id`) ausgeschlossen; Überlappung ≥ 50 % der kürzeren Dauer → `tempo_dup` = `KEY hh:mm–hh:mm Text(80)`; Nutzer wählt „trotzdem“ (`tempo_dup='ok'`) oder „ausnehmen“ (P:939-946).
- Wiederholungen/Rate-Limit: nur bei HTTP 429, bis 3 Wiederholungen, Wartezeit `min(30 s, max(2 s, Retry-After oder 4 s·(Versuch+1)))` (P:120-128). Pause 120 ms zwischen Worklogs (P:711). Kein Wiederholen bei 5xx/Netzfehlern (nur Fehler speichern). Bei Status 0/401/403 Abbruch des ganzen Laufs (P:709).
- Fehlerbehandlung je Eintrag: Fehlertext in `tempo_push_error`; 400/403 mit LOCK_RE (`period|periode|zeitraum|gesperrt|locked|approved|genehmigt|submitted|eingereicht|frozen`) → ganzer Monat in `tempo_months` als gesperrt (`lockFromError`), Eintrag übersprungen (P:705-707). 400 mit `originTaskId|issue not found` bei Ticket-Ziel: Hinweistext „Plane-Ticket? Work-Item verknüpfen“ (P:702).
- Auslöser: manuell (`POST /api/integrations/tempo-push/push`, optional `ids`) oder Modus `auto` (`tempo_push_mode`): alle 10 min `scheduleAuto`, nach 60 s Verzögerung, nur Buchungen, deren Ende > 5 min zurückliegt (P:720-725). Freigabe-Modell: lokal zuerst, dann manuelle/automatische Freigabe (P:10).
- Jira-Ticket anlegen (P:403-424): zuerst mit `assignee:{name:worker}`, bei 400 ohne Assignee wiederholen; meldet Jira ein Pflichtfeld `customfield_N` mit „account“, Account-ID bestimmen und Wert als Zahl, String oder `{id}` probieren (`accountIdFor`, Cache je Projektkürzel).
- Gegenstück je Plane-Ticket (Modus `ticket`, `ensureJira`, P:427-450): Summary `[PLANEKEY] Titel` (≤250), vorher JQL-Suche `project="<newProject>" AND summary ~ "\"[KEY]\""`, Description „Gegenstück zum Plane-Ticket … Angelegt von Zeitprotokoll“; Key in `tasks.jira_link` speichern (auch für alle Tasks mit gleichem `plane_work_item_id`).
- Sammelticket je Projekt (`ensureCollector`, P:453-473): Summary `Zeitprotokoll: <Projekt> – Sammelbuchungen`, in `projects.tempo_issue` gemerkt; Wiederfinden per exakter Summary-Gleichheit.

## 3. Übergangs-Ticket je Person und Monat (Plane-Zeiten)

- Aktiv bei `tempo_plane_mode` = `month` (Default; Alternative `ticket`) (P:75).
- Name: `Übergang Zeiten <jira_username> <YYYY-MM>` (P:479); Projekt `jira_new_project`, Typ `jira_new_issuetype` (Default „Task“), dem Nutzer zugewiesen (über `createIssue` assignee), ohne `jira_new_project`/`worker` kein Ziel (Status `needs`, P:174).
- Anlage (`ensureMonthTicket`, P:478-496) beim ersten Buchen des Monats: erst `tempo_month_tickets` (month, worker), dann JQL-Suche `project=… AND summary ~ "\"<Summary>\""` mit exakter Trim-Gleichheit, sonst anlegen; Eintrag `tempo_month_tickets(month, worker, issue_key)`.
- Beschreibung (`updateMonthDescription`, P:498-522) wird nach Pushs, die den Monat berührt haben, per PUT `issue/{key}` aktualisiert: Jira-Wiki-Tabelle `||Plane-Ticket||Titel||Projekt||Stunden||Buchungen||zuletzt||`, Zeilen je Plane-Ticket (Summe Std. auf 0,1, `de-DE`), Kopf „Summe: X h in N Buchungen“; Sonderzeichen `| [ ] { }` ersetzt; Link `<planeUrl>/browse/<KEY>/` mit `planeUrl = plane_base_url/plane_workspace`; Änderungserkennung per `desc_hash`. Datenbasis: Buchungen mit `tempo_push_target = issue_key` im Monat.
- Kunde/Account: steht **nicht** im Ticket, sondern im Tempo-Account des Worklogs (Buchung > Projekt > Standard), Plane-Infos stehen im Worklog-Kommentar (Text im Ticket, P:489).
- Schließen (`closeMonthTickets`, P:525-550): alle 6 h und am Ende jedes Pushs, nur Monate < aktueller Monat, `closed_at IS NULL`. Bedingung: Monat in `tempo_months` gesperrt **oder** Nachfrist vorbei (`tempo_month_close_days`, Default 5, 0-31; ab Monatsletzter + N Tage). Bei bloßer Nachfrist nur, wenn kein offener Eintrag (`new/changed/error/needs`, Art `month`/`create-month`) mehr existiert. Dann Beschreibung aktualisieren, GET Transitions, Übergang wählen: `to.statusCategory.key == "done"`, sonst Name passt `DONE_RE` (`erledigt|fertig|done|closed|geschlossen|resolved|abgeschlossen`); POST Transition; `closed_at` setzen; Fehler → `close_error`. Kein Resolution-Feld wird gesetzt.
- Nachträge in einen geschlossenen Monat: unklar (Ticket wird nicht wieder geöffnet; `monthTicketOf` liefert weiter den Key; Tempo kann auf geschlossene Tickets ggf. nicht buchen).

## 4. Monatssperre, Altdaten-Grenze, Sync Tempo → lokal

- **Altdaten-Grenze 01.10.2026**: nicht im Code festverdrahtet. Sie ist der Einstellungswert `tempo_push_since` (Format `YYYY-MM-DD`; Default sonst 1. des aktuellen Monats, P:60). Beleg: Konzept (`Zeitprotokoll Team-Server – Konzept.md` Zeile 190-191: „Nach Tempo zählt nur ab 01.10.2026; die Zeit davor kommt später per sauberem Grundimport aus Tempo“). Wirkung: `candidates` filtert `date >= since` (P:229-232); `monthend.js:28` (Default „2000-01-01“ falls leer), `sources.js:348`, `assistant.js:90`, `tempo-sync.js:107` nutzen ihn ebenfalls. Go-Portierung: je Server-/Benutzereinstellung `tempo_push_since`.
- **Monatssperre** (`tempo_months`: month, locked, reason, remote_count, checked_at): gesetzt durch (a) Stundenzettel-Status aus `timesheet-approval/current` mit Status `approved|waiting|submitted` (Gründe „Stundenzettel genehmigt/eingereicht“), (b) Fehler beim Pushen (LOCK_RE, 400/403). Prüfintervall 30 min je Monat; Fehler in der Statusabfrage werden verschluckt (Tempo-Fehler 401/403 setzen `locked` nicht). Aufheben: `DELETE /api/integrations/tempo-push/months/{month}` (löscht Eintrag, löscht Fehler der ungepushten Einträge). Gesperrte Einträge: Status `locked`, nicht gepusht, nicht automatisch erneut versucht. Prüfung lokaler Änderungen in gesperrten Monaten auch in `checks.go:216` (Go, `tempo_months`).
- **Tempo → lokal** (`tempo-sync.js`, nur lesen, Schreibquelle `source='tempo'`):
  - Holt eigene Worklogs je Kalendermonat (`worklogs/search`, `worker:[key]`; „Tempo DC mag keine Riesenabfragen“, S:77) ab `tempo_sync_from` (Pflicht beim ersten Lauf); automatisch alle 15 min geprüft, ausgeführt wenn letzter Lauf ≥ 12 h her; Fenster: Vormonat 1. bis heute, mindestens `tempo_sync_from` (S:195-201). Ein Lauf gleichzeitig (`running`, sonst 409). Vor dem Lauf Sicherung (`createBackup("vor-tempo-abgleich")`).
  - Normalisierung (S:94-100): Datum/Zeit aus `started`/`dateStarted` (Leerzeichen→`T`), ID = `tempoWorklogId ?? id`, Dauer `timeSpentSeconds`, Ticket `issue.key ?? originTaskId`, Account aus `attributes._Account_.value` (oder erstem Attribut mit „account“ im Namen/Key), Autor `worker ?? author.key ?? author.name`.
  - Fremde Autoren überspringen (`foreign_skipped`); Eigene Übertragungen (`tempo_push_id` bekannt) überspringen (`own_skipped`); doppelt gelieferte IDs ignoriert.
  - Fehlt die Uhrzeit (`00:00:00`), werden Einträge je Tag ab 08:00 hintereinandergelegt (Cursor). Start/Ende als ISO (UTC aus lokaler Zeit).
  - Idempotenz: Schlüssel `time_entries.tempo_worklog_id` (UNIQUE, in Go `UNIQUE(user_id,tempo_worklog_id)`). Vorhanden: UPDATE bei Änderung von Datum, Dauer, Kommentar, Task oder Account; sonst INSERT (`billable=1`).
  - Projekt: `sources.projectByJiraKey(Prefix)`; fehlt es → Projekt per `GET /project/{key}` benennen, vorhandenes gleichnamiges nehmen oder neu anlegen mit `status='archived'`, als Jira-Quelle verknüpfen (`linkBy("jira",…)`). Task: `tasks.jira_ticket = Key` im Projekt, sonst global, sonst neu (`status='done'`); Titel wird aus `issue.summary` nachgetragen, wenn leer/gleich Key.
  - In Tempo gelöscht → lokal löschen: nur für vollständig gelesene Monate und nur `source='tempo'`.
  - Danach: Feiertage Niedersachsen ergänzen (`ensureHolidays`), `tracking_start_date` ≤ from setzen, `tempo_sync_from`/`tempo_sync_last` speichern, `absences.fromTempo()` (Urlaub/Krank aus Abwesenheitsprojekt).
  - **Grundimport** (`replaceOld`): lokale Buchungen mit `date` zwischen `from` und `tempo_push_since`, `source <> 'tempo'`, `tempo_push_id IS NULL` werden gelöscht und durch Tempo-Zeiten ersetzt (Warnlog, Sicherung vorher). Ohne Flag nur gezählt (`old_local`).
- Alter CSV/Import-Weg (SV:1278-1420): paginierte Abfrage, Autor-Filter auf `author.name/key`, Duplikat über `tempo_worklog_id`, Projekt über Jira-Kürzel; Zeit aus `dateStarted` (Default 09:00:00), Quelle `tempo`. Wird vom neuen Abgleich (S) abgelöst; in Go nicht nachbauen (S genügt).
- „Meine Jira-Tickets“ (`syncMyTickets`, P:579-611): alle 30 min (Start nach 30 s), JQL `jira_my_jql` (Default `assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC`) in 100er-Seiten bis 1000 Ergebnisse; stumm geschaltete Projekte (`project_sources.status='ignored'` oder `projects.muted=1`) überspringen; je Ticket `ensureTaskForIssue` (Task mit `jira_link`, `ticket_system='jira'`, `jira_mine=1`, ggf. Projekt anlegen); nicht mehr zugewiesene: `jira_mine=0`, Status `done` bei `ticket_system='jira'`. Zusätzlich Projektkürzel in `jira_project_keys` merken (Erkennung „ist Jira-Key“).

## 5. Datenmodell und Einstellungen

Einstellungen (`settings` key/value; im Go-Server je Benutzer bzw. Serverkonfiguration; `jira_token` verschlüsselt, `SECRET_KEYS` SV:735; Go: `user_secrets` kind `jira_token`, 001_users.sql:31):
`jira_base_url`, `jira_token`, `jira_username`, `jira_userkey`, `jira_enabled` (≠"0"), `jira_my_tickets` (≠"0"), `jira_my_jql`, `jira_project_keys` (CSV, automatisch), `jira_new_project`, `jira_new_issuetype` (Default Task), `tempo_push_enabled` ("1"/"true"), `tempo_push_mode` (`auto`/`manual`), `tempo_push_since`, `tempo_booking_issue` (globales Sammelticket), `tempo_account` (Standard-Account), `tempo_account_attr_id` (Default 2), `tempo_plane_mode` (`month`/`ticket`), `tempo_month_close_days` (Default 5), `tempo_sync_from`, `tempo_sync_last`, `tracking_start_date`, `plane_base_url`, `plane_workspace` (für Links), `output_rule_default` (Default `bridge`).

Ausgaberegel (SO:378-388): `ruleOf(project)` = `projects.output_rule` (≠ `default`) sonst `output_rule_default`; `allowsPlane` = Regel `bridge|plane` und Projekt wirklich in Plane (`plane_project_id` gesetzt, keine als `gone` markierte Plane-Quelle); `allowsTempo` = Regel `bridge|tempo`.

Tabellen/Spalten (Node, ALTER in P:28-44 und SV:2638/2708; Go-Gegenstück in `internal/db/migrations/002_core.sql` und `003_integrations.sql` ist bereits vorhanden):
- `time_entries`: `tempo_worklog_id` (INTEGER UNIQUE; Spiegel), `tempo_push_id` (TEXT), `tempo_push_hash`, `tempo_push_target`, `tempo_push_synced_at`, `tempo_push_error`, `tempo_skip`, `tempo_dup`, `tempo_target`, `tempo_account`, `source`, `check_flags`/`check_note`, `is_absence`.
- `projects`: `tempo_issue` (Sammelticket), `tempo_account`, `jira_project`, `jira_key`, `output_rule`, `plane_project_id`, `muted`.
- `tasks`: `jira_ticket`, `jira_link` (Jira-Gegenstück/Ziel), `jira_origin`, `jira_mine`, `ticket_system` (`jira`), `plane_work_item_id`.
- `tempo_push_deletions(id, worklog_id, label, created_at, error)`.
- `tempo_month_tickets(month, worker, issue_key, created_at, closed_at, close_error, desc_hash; PK(month,worker))`.
- `tempo_months(month PK, locked, reason, remote_count, checked_at)`.
- `ticket_map(jira_key PK, status 'mapped'|'skipped', plane_key, plane_work_item_id, plane_project_id, title, mapped_at)` (PL:56; Zuordnung Jira-Altticket → Plane-Work-Item).
- `project_sources` (Quelle `system='jira'`, external_id = Projektkürzel, status/ignored) (SO:22).
- Im Go-Schema laufen `tempo_*`-Tabellen schon mit `user_id` (Mehrmandanten: alle Schlüssel je Person, z. B. PK `(user_id, month)`; `worker`-Spalte bleibt zu prüfen: unklar, ob nötig).

Routen (Node), ohne Prefix `jira.js` (J): `GET /api/jira/status|search|issue/:key|projects`, `POST /api/jira/issue/:key/comment|transition|task`, `POST /api/jira/issues`, `PUT /api/jira/projects`. Tempo-Push (P): `/api/integrations/tempo-push/` `status`, `autosetup` (GET+POST), `accounts`, `entry-account`, `outbox`, `push`, `skip`, `deletions/:id` (DELETE), `check`, `dup`, `months/:month` (DELETE), `project/:id` (PUT), `suggest`, `target`; `/api/integrations/jira/my-tickets/sync|status`. Sync (S): `GET/POST /api/tempo-sync`.

Ticket-Vorschläge (`suggest`, P:269-347, optional nachzubauen): lokaler Korpus aus importierten Worklogs (400 Tage), Jira-Aufgaben und `ticket_map`; Score `0.6·Textähnlichkeit + 0.22 (gleiches Projekt) + 0.1·Häufigkeit + 0.08·Aktualität`, Schwelle 0.3; bei Score < 70 zusätzlich JQL `text ~ "<4 Wörter>"` (im verknüpften Jira-Projekt). `textmatch.js` ist in Go bereits als `textmatch.go` da.

Autosetup (P:765-833): schlägt `jira_new_project` (Präferenzen ZEIT, TIME, ZP, ORGA, INT, PRJB, ADM, IT) + Issue-Typ (Task/Aufgabe, sonst erster Nicht-Subtask) über `createmeta`, Account-Attribut-ID, häufigste Accounts aus Worklogs der letzten 183 Tage (gesamt und je Projekt) sowie `since` (ältester nicht gesperrter der letzten 3 Monate) vor.

## 6. Fallstricke und Sonderfälle (belegt)

1. `worker` muss der Jira-**Key** (JIRAUSER…) sein, nicht der Login (P:65). Autorvergleich im Alt-Import akzeptiert Name oder Key (SV:1334); im Sync zusätzlich `jira_username` (S:125).
2. POST-URL endet mit `/worklogs/` (Slash), PUT/DELETE ohne Slash (P:687 vs. 682).
3. `started` ohne Zeitzonenangabe, lokale Zeit des Nutzers; beim Lesen kommt `+0000`-Format (SV:1184), Leerzeichen statt `T` möglich (P:251, S:95). Tempo liefert oft 00:00 → Pseudo-Zeiten ab 08:00 (S:121-131). Beim Duplikat-Vergleich `Date.parse` der Roh-Zeitstrings (P:251; Zeitzonenabhängigkeit: unklar).
4. Mindestdauer 60 s, Rundung auf Minuten; in Tempo wird die Stückelung nie zusammengefasst (1 Buchung = 1 Worklog).
5. Account ist Pflichtattribut bei Beispiel; Fehlermeldung wird per Regex `/account/i` erkannt (P:690). Attribut-ID 2 = Standardannahme. Auf eigenem Jira-Ticket wird Account bewusst nicht gesendet (Tempo nimmt Ticket-Account).
6. Pflichtfeld Account beim Ticket-Anlegen als `customfield_N`: Wert-Format variiert (Zahl, String, `{id}`), daher 3 Versuche (P:414). Anlegen mit Assignee kann scheitern → zweiter Versuch ohne Assignee (P:404).
7. Ticket-Summary-Suche per JQL `summary ~ "\"…\""` ist unscharf → Ergebnis wird zusätzlich lokal exakt/`startsWith` verglichen (P:437, 461, 485); JQL-Zeichen `"` und `\` werden ersetzt (J:50, P:460).
8. Stundenzettel-Freigabe-Abfrage und Worklog-Suche schlucken Fehler (`catch (_)`); 401/403 dort führen nicht zur Sperre (P:245, 259). Eine Sperre kann nur durch Tempo-Fehlermeldung (Textmuster) erkannt werden: Sprachabhängig (de/en).
9. Löschen nie hart: Löschauftrag wird vorgemerkt (`tempo_push_deletions`) und bei nächster Übertragung ausgeführt; im gesperrten Monat bleibt er stehen, blockiert aber nicht die anderen (P:640-641).
10. Gelöschter Worklog in Tempo: PUT 404 → als neu anlegen (P:683); DELETE 404 → ok.
11. Ticket/Gegenstück: `UPDATE tasks SET jira_link … WHERE plane_work_item_id=? OR id=?` – gleiche Plane-Work-Items teilen das Gegenstück (P:448, 357).
12. Monatsziel bleibt bei früher übertragenen Worklogs bestehen (kein Umhängen auf Übergangsticket nach Moduswechsel, P:172).
13. Tempo-Rolle/Rechte: 403 ohne Sperrtext bricht den Lauf ab (P:641, 709), mit Sperrtext Monat sperren.
14. Zertifikate: Firmen-CA und Kettenreparatur (`withChainRepair`), TLS-Fehlertext mit Hinweis (P:99, 115). In Go über `crypto/tls` mit System-/Firmen-CA lösen (unklar, ob Zusatz-CA-Datei nötig).
15. Tempo-Spiegel zählt für Konto als „bestätigte“ Zeit (SV:959-961, in Go `konto.go:35`). `norule`/`review` verhindern Push (Prüfregeln aus `checks.js`).
16. Jira-Key-Regex überall `^[A-Z][A-Z0-9_]*-\d+$` (Großbuchstaben); Jira-Projektkürzel mit kleinen Buchstaben oder Ziffern am Anfang werden nicht erkannt.
17. Kommentar SV:1136 vs. Code zum Token siehe 1 (unklar).
18. Ein Hintergrundlauf zur Zeit pro Prozess (`pushing`, `running`); im Mehrbenutzer-Go-Server pro Person (`planeState`-Muster) und mit Mutex bzw. Datenbank-Sperre ausführen.
19. Nicht belegt: Verhalten bei Zeitzonenwechsel/Sommerzeit, Pagination der Worklog-Suche im Push-Pfad (kein `limit/offset`, vermutlich Standardgrenze der Tempo-API; „unklar“), 429-Header `Retry-After` bei Jira DC.

## 7. Hinweise für die Go-Umsetzung (Muster Plane)

- Client analog `plane_client.go`: je Person Drossel, `Retry-After` bei 429 (hier: max. 3 Wiederholungen), Adresse (`jira_base_url`) aus Server-Einrichtung statt Benutzer (SSRF), https Pflicht, Token je Person aus `user_secrets` (`Secret("jira_token")`).
- Sync/Push analog `plane_sync.go` (`planeState` mit Mutex, `again`, Timer): `tempoState` mit Auto-Timer 60 s nach Änderung, Cron 10 min (Push), 30 min (Meine Tickets), 6 h (Monatstickets schließen), 12 h (Tempo-Abgleich).
- Hash-Algorithmus (`sha1(JSON.stringify([...]))[:16]`) nicht übernehmen müssen, solange kein gemeinsamer Datenbestand mit dem Node-System weiterläuft; bei Migration bestehender `tempo_push_hash` führt ein anderer Hash zu „geändert“ und PUT (unkritisch, aber Massen-Änderungen vermeiden: Hash kompatibel halten oder beim Import neu setzen).
