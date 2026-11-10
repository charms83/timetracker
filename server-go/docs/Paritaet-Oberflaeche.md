# Parität: Oberfläche (frontend/) ↔ Go-Server (Stand 10.11.2026)

Grundlage: alle `api(METHODE, Pfad)`-Aufrufe in `frontend/src/*.jsx` (86 Pfade), verglichen mit den Routen des Go-Servers. Per Mustersuche ermittelt, nicht fehlerfrei; Pfade mit Platzhaltern können falsch als fehlend oder vorhanden erscheinen. Spalte „Go heute“: vorhandene Entsprechung unter anderem Namen (dann genügt eine Weiterleitung/ein Alias).

Ziel: Der Go-Server liefert die bestehende Oberfläche aus (`ZP_STATIC_DIR`) und beantwortet dieselben Pfade; die Oberfläche bekommt nur Anmeldung und „braucht Client“-Hinweise. Der Client (Electron/Mac) behält nur lokale Teile (A).


## A Bleibt im Client (lokaler Helfer, nicht im Server) – 11

| Pfad | Methode | Genutzt in | Go heute |
|---|---|---|---|
| `/meetings/config` | GET | zp-assistant |  |
| `/meetings/events` | GET | zp-calendar, zp-meetings |  |
| `/meetings/now` | GET | zp-meetings |  |
| `/meetings/status` | GET | zp-assistant, zp-meetings |  |
| `/meetings/suggest-task` | GET | zp-calendar |  |
| `/meetings/suggestions` | GET | zp-meetings |  |
| `/pair/clients` | GET | zp-addon |  |
| `/pair/info` | GET | zp-addon |  |
| `/security/tls` | GET | zp-jira, zp-tempo |  |
| `/ssh-sessions` | GET/PUT | App |  |
| `/ssh-tracking` | GET/POST | App |  |

## B Jira/Tempo (Schritt 1) – 8

| Pfad | Methode | Genutzt in | Go heute |
|---|---|---|---|
| `/integrations/jira/my-tickets/status` | GET | zp-tempo | gebaut 09.11. (`last` bleibt null, Abruf fehlt) |
| `/integrations/tempo/import/cancel` | POST | App |  |
| `/integrations/tempo/status` | GET | App | gebaut 09.11. (bei mehreren Instanzen `?instance=`) |
| `/jira/projects` | GET | zp-jira | gebaut 09.11. (+ `instance_id` je Projekt) |
| `/jira/search` | GET | zp-ui | gebaut 09.11. (ohne `?instance=` alle nutzbaren Instanzen) |
| `/jira/status` | GET | zp-jira | gebaut 09.11. (Modul aus → `{enabled:false}`) |
| `/tempo-csv-import` | POST | App | bewusst Node-only; am Team-Server: `/tempo-sync` |
| `/tempo-sync` | GET/POST | zp-tempo-sync | gebaut (`tempo_sync.go`, 12‑h-Cron); CSV-Import lokal bleibt Node-only, Server nutzt API-Abgleich |

## C Plane (Schritt 1) – 2

| Pfad | Methode | Genutzt in | Go heute |
|---|---|---|---|
| `/integrations/plane/map` | GET | zp-tempo | gebaut 09.11. |
| `/integrations/plane/my-items` | GET | zp-ui | gebaut 09.11. (Cache je Person, nicht über Neustart) |

## B2/C2 Nachgetragen (09.11.2026): von der Oberfläche genutzt, fehlten in der Liste (Pfade mit Platzhaltern)

| Pfad | Methode | Genutzt in | Go heute |
|---|---|---|---|
| `/jira/issue/{key}` | GET | zp-jira |  |
| `/jira/issue/{key}/transition` | POST | zp-jira |  |
| `/jira/issue/{key}/task` | POST | zp-jira (Timer starten) |  |
| `/jira/issue/{key}/comment` | POST | zp-jira (laut backend/jira.js) |  |
| `/jira/issues` | POST | zp-jira, zp-ui |  |
| `/jira/projects` | PUT | zp-jira (Verknüpfen) |  |
| `/integrations/jira/my-tickets/sync` | POST | zp-tempo |  |
| `/integrations/tempo-push/autosetup` | GET/POST | zp-tempo |  |
| `/integrations/tempo-push/accounts` | GET | zp-tempo |  |
| `/integrations/tempo-push/entry-account` | POST | zp-tempo |  |
| `/integrations/tempo-push/skip` | POST | zp-tempo |  |
| `/integrations/tempo-push/check` | POST | zp-tempo |  |
| `/integrations/tempo-push/project/{id}` | PUT | zp-tempo |  |
| `/integrations/plane/map/suggest` | GET | zp-tempo |  |
| `/integrations/plane/map/apply` | POST | zp-tempo |  |

Vorhanden, aber mit abweichender Antwort: `/integrations/tempo-push/status` (Go liefert je Instanz `instances[]`, Node oben `ok/user/issues`); `/integrations/tempo-push/outbox` hat `suggest` und `gaps` aus `dayOverview` (wie Node).

## D Arbeitsalltag (Schritt 2) – gebaut 10.11.2026

Umsetzung: `internal/app/todo.go`, `monthend.go`, `gapsuggest.go`, `assistant.go`, `workday.go`, Hilfen `alltag.go`, Migration `017_alltag.sql`. Alle Routen nur eigene Daten (kein `?user=`).
Vorschläge („Light“, Regeln/Textabgleich, keine KI) folgen einem gemeinsamen Schalter mit vier Stufen (`suggestOnFor`, tempo_tickets.go): Konto erfasst Zeiten (nicht lokales Konto mit productive=false) · `module_suggest` · `users.suggest_blocked` · `suggest_enabled`. Abgeschaltet: keine Abfragen, leere Antwort mit `suggest:false`/`reason` (Listen-Routen: `[]`).

| Pfad | Methode | Genutzt in | Go heute |
|---|---|---|---|
| `/todo` | GET/POST | zp-todo, zp-ui | gebaut (Vorlagen + „Zeiten nachtragen“ wie Node; `capacity.meetings` immer 0) |
| `/todo/{id}` | PUT/DELETE | zp-todo, zp-calendar | gebaut (Löschen mit Buchung in gesperrtem Monat → 409 month_locked) |
| `/todo/{id}/start` | POST | zp-todo, zp-calendar | gebaut (Timer über gemeinsamen `startTimer`) |
| `/todo/range` | GET | zp-calendar | gebaut |
| `/todo/suggest`, `/todo/suggest/dismiss` | GET/POST | zp-todo | gebaut (Light, abschaltbar; ohne Termin-Vorbereitung) |
| `/todo/templates`, `/todo/templates/{id}` | GET/POST/DELETE | zp-todo | gebaut |
| `/todo-settings` | PUT | zp-todo | gebaut – Schalter gespeichert, Abgleich nach Plane/Jira/Tempo **offen** (`settings.sync_available:false`) |
| `/assistant` | GET/PUT | App, zp-assistant | gebaut (regelbasiert, abschaltbar; aus = leeres Gerüst `enabled:false`) |
| `/assistant/tasks`, `/assistant/reset` | GET/POST | zp-assistant | gebaut: tempo_sync, duplicates, cleanup, tempo_noticket; selfcheck nur Admin (`/admin`) |
| `/workday` | GET | App | gebaut |
| `/workday/start`, `/workday/{id}`, `/workday/end`, `/workday/reopen`, `/workday/pause/start`, `/workday/pause/end` | POST/PUT | (Oberfläche nutzt sie nicht) | gebaut; Pausen ohne Arbeitstag-Bezug, Monatssperre geprüft |
| `/month-end`, `/month-end/ok` | GET/POST | zp-gaps | gebaut (+ `approval`, `locked_months`; gesperrte Monate nicht nachtragbar; ohne „am Mac von–bis“) |
| `/missing-hours` | GET | zp-ui | gebaut (überspringt abgeschlossene und gesperrte Monate) |
| `/gap-suggest`, `/gap-suggest/learn` | GET/POST | zp-gaps, zp-tempo, zp-ui | gebaut (Light: Lücken, typischer Tag, Rest; abschaltbar) |
| `/suggest-now` | GET | zp-ui | gebaut (Light: Gewohnheit, früher gewählt, zuletzt heute) |
| `/absence-hints`, `/absence-hints/{key}` | GET/POST | zp-assistant | gebaut (liefert gespeicherte Hinweise; Übernahme unter Monatssperre) |
| `/ticket-estimates`, `/ticket-estimates/{ticket}` | GET/PUT | zp-report, zp-ui | gebaut |

Bleibt im Client (keine Erkennung im Server): Termin-Vorschläge und -Vorbereitung (Kalender), SSH-Vorschläge und Verknüpfen von SSH/Termin beim Lernen, „am Mac von–bis“ (Anwesenheit), Abwesenheits-Erkennung aus dem Kalender. Grund: lokales Wissen des Rechners. Offen: Annahme-Schnittstelle, über die der Client erkannte Abwesenheits-Hinweise ablegt; Tempo-Abwesenheiten als Hinweis (braucht Tempo-Import); `/ticket/ensure`, `/ticket/start` – gebaut 10.11. (siehe E).

## E Betrieb/Aufräumen (Schritt 3) – 13

| Pfad | Methode | Genutzt in | Go heute |
|---|---|---|---|
| `/admin/actions` | GET | zp-server |  |
| `/admin/backup` | POST | zp-server |  |
| `/admin/reset-range` | DELETE | App |  |
| `/admin/server-info` | GET | zp-server | /api/admin/status |
| `/audit` | GET | zp-calendar | gebaut 10.11. (`audit_view.go`): nur eigene Buchungen, Format wie Node (`rows/projects/tasks/total`, vorher/nachher nur COLS); `actor` = Name bei Nachtrag |
| `/audit/verify` | GET | zp-calendar | gebaut 10.11.: Kette global, schrittweise geprüft (Stand im Speicher, stündlich von vorn, `complete`), `rows` = eigene Einträge; Bruch bei fremder Zeile ohne Id/Zeitpunkt; `file`/`file_ok` immer null |
| `/check/integrity` | GET | App | gebaut 10.11. (`integrity.go`): nur eigene Daten; Überschneidungen/Pausen letzte 365 Tage, sonst ≤ 500 Treffer |
| `/cleanup/local-projects`, `/cleanup/apply`, `/cleanup/allow-local` | GET/POST/PUT | zp-cleanup | gebaut 10.11. (`cleanup.go`): je Person, eine Transaktion, Monatssperre → 409; ohne Datei-Sicherung (`backup: null`) |
| `/cleanup/search` | GET | zp-cleanup | gebaut 10.11.: eigene Aufgaben + Jira (nur Modul an, alle nutzbaren Instanzen) |
| `/logs` | DELETE/GET | App | /api/admin/logs |
| `/logs/cleanup` | POST | App |  |
| `/selfcheck` | GET | zp-server | /api/admin/selfcheck |
| `/selfcheck/run` | POST | zp-server | POST /api/admin/selfcheck |
| `/projects/{id}/merge`, `/projects/adopt-names` | POST | zp-projects, zp-assistant | gebaut 10.11. (`project_tools.go`) |
| `/sources/duplicates` (+ Feld `duplicates` in `GET /sources`), `/sources/duplicates/ignore` | GET/POST | zp-projects, zp-assistant | gebaut 10.11. |
| `/sources/local-times`, `/sources/local-times/assign` | GET/POST | zp-assistant | gebaut 10.11. (assign lässt gesperrte Monate aus: `skipped_locked`) |
| `/ticket/ensure`, `/ticket/start` | POST | zp-gaps, zp-ui | gebaut 10.11. (`ticket_routes.go`): legt nie ein Ticket in Plane/Jira an, nur die lokale Aufgabe; Sperre je Person gegen Doppelanlage; neue Jira-Aufgabe nur mit Modul an |

Nicht im Mitarbeiter-Server (bewusst): `/admin/actions`, `/admin/backup`, `/admin/reset-range`, `/admin/server-info`, `/logs`, `/logs/cleanup`, `/selfcheck*` (→ `/admin`), Tempo-Rückimport, Cloud. Neu für Teamleiter/Personal: `/api/team/overview`, `/api/team/members`, `/api/personal/overview`, `/api/personal/people` (`team_view.go`).

## Reihenfolge
1. B + C (Jira/Tempo/Plane – Jira bald, Plane Hauptweg)
2. D (To-do, Assistent, Arbeitstag, Monatsende, Lücken, Vorschläge, fehlende Stunden, Ticket-Schätzungen)
3. E (Aufräumen, Audit prüfen, Logs/Selbsttest/Server-Info als Alias, Sicherung)
4. Oberfläche: Anmeldung (401 → `/login`), „braucht Client“-Hinweise, „Server aktualisieren“ entfernen; Electron: Server-Adresse statt lokalem Node-Server; lokaler Helfer (A) mit Geräte-Token.
Hinweis: Meetings/SSH/Kopplung bleiben lokal – der Server braucht nur die allgemeine Annahme-Schnittstelle für Vorschläge (siehe Gesamt-Spezifikation).
