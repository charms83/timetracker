# Zeitprotokoll – Übergabe für Claude Code stand 30.10.2026, Version 6.36.0

## Projekt
Mac-Zeiterfassung "Zeitprotokoll" (Max Mustermann / Beispiel). Pfad: ~/zeitprotokoll.
Stack: Node 22 + Express + node:sqlite (127.0.0.1:3747), React/Vite-Frontend, Electron-Tray-App (desktop/main.js).
DB: ~/Documents/Zeitprotokoll/zeitprotokoll.db. Plane (plane.example.com, Workspace "example") ist das Hauptsystem, Jira DC + Tempo übergangsweise. Später ein Go-Teamserver.
Produktziel: für Menschen mit ADHS gebaut.
Neustart: `lsof -ti tcp:3747 | xargs kill; cd ~/zeitprotokoll && bash install.sh` (Electron: ⌘Q, Browser: ⌘R).

## Feste Regeln des Nutzers
- Auf Deutsch antworten.
- Jede neue Funktion ins Konzeptdokument (Claude Docs id 7d6fcd48-28e5-4b7f-bb55-9cf25af96e5a, Version steht bei 6.36.0).
- Sicherheit/ISO 27001: Daten nur für den Nutzer selbst und Berechtigte, nichts darf abgegriffen werden können.
- Begründungen/Protokolltexte (ISO-Audit) nie automatisch mit inhaltlichen Aussagen vorbelegen.
- Urlaub: nur Programm/Kalender zählen; Tempo-HAP-Urlaub (`is_absence=1`) wird nur vorgeschlagen.
- Tempo: keine Buchung ohne Jira-Ticket. Nur Plane-Buchungen gehen aufs Sammelticket (Monatsticket).
- Server-Installer mit Auswahldialogen, nicht Semaphore/Ansible. Safari: kein Add-on.
- React: nie `[]`/`{}` als Standardwert eines Props in useEffect-Abhängigkeiten. Bei "langsam" zuerst Server messen, dann Requests im Browser zählen.

## Architektur-Kernpunkte
- Module: lazy `ensure()`, beim Registrieren nie die DB anfassen. Hooks: sources, checks, presence, cleanup, plane, todo, monthend, absences, tempoSync, tempoPushRef.
- Einträge haben `source` (manual, meeting, plane, tempo, tempo_csv) + externe IDs. Tempo-Spiegel ist read-only.
- Tempo-Push (backend/tempo-push.js `targetOf`): eigenes Jira-Ticket → dieses Ticket. Plane-Buchung → Monatsticket. Sonst Kind "no-ticket" (Status "needs", Assistenten-Aufgabe `tempo_noticket`).
- Vorschlagsalgorithmus (backend/monthend.js `suggest`): Scores aus letzten Buchungen, gleicher Wochentag/Zeit, Nachbareinträgen, gelernten Entscheidungen (Tabelle suggest_feedback, nur n≥2, letzte 90 Tage, leicht gewichtet), typischer Tag (letzte 6 gleiche Wochentage, ≥3 von 6, Blöcke ≥30 min). Vorschläge werden so gekürzt, dass sie sich nie mit Buchungen/Pausen/anderen überlappen. Endpunkte: `/api/gap-suggest`, `/api/gap-suggest/learn`, `/api/suggest-now`.
- Plane-Client (plane.js): Pacing (Hintergrund ≤45/min, interaktiv ≤58/min mit fail-fast), `blockedUntil` nach 429, Single-Flight für my-items mit persistiertem Cache, Resolve prüft lokale Tasks zuerst und cached "nicht gefunden" 10 min.
- Frontend-Events: `zp:edit-entry` (detail `{id,date}` oder `{new:true,date,start,end}`), `zp:entry-changed`. Globales TimeEntryModal in App.jsx (`anyEdit`). Helfer `deleteEntry`, `dupIds` aus zp-ui.jsx. `askConfirm` liefert `{reason}`, `{adjust:{start,end,other}}` oder null. fetch bricht nach 30 s ab. App lädt neu, wenn /api/health eine neue Version meldet.
- Lokale API-Sicherheit (pairing.js): Host-Check, CORS nur eigene UI/null mit App-Token bzw. gepaarte Extensions.
- Leistungs-Log: Requests >700 ms und Event-Loop-Lag >300 ms landen in system_logs, Kategorie "leistung".
- Einstellungen: `ist_from_workday` ("1" = Arbeitstag-Fenster zählt als Ist, Standard aus), `workday_prompt` ("1" = beim Login fragen, Standard aus), `cfg.mainInBrowser` (Desktop, Standard true).

## Wichtige Dateien (zuletzt geändert)
backend: server.js, monthend.js, tempo-push.js, presence.js, plane.js, todo.js, assistant.js, checks.js
frontend/src: App.jsx, zp-gaps.jsx (Nachtragen), zp-ui.jsx, zp-tempo.jsx (Übertragen), zp-assistant.jsx, zp-calendar.jsx, zp-day.jsx, zp-week.jsx, zp-todo.jsx
desktop: main.js, ui/mini.js · install.sh · scripts/startup.sh

## Zuletzt erledigt (6.25 → 6.36)
Urlaubsregel, neue Wochenansicht, "Nachtragen" statt Lücken füllen (pro Tag Arbeitszeit/Mac-Präsenz/Buchungen/Vorschläge, Saldo nur als Hinweis), Kalender-Abo per http-URL kopieren, Assistent mit Lernalgorithmus und "Jetzt vermutlich", Tempo-Regeln, "Zeiten anpassen" bei Überlappung, Ticketsuche in Vorschlägen, großes Fenster = Browser, Performance-Fixes (6.32.x: Endlosschleife in TicketInput, Plane-429-Handling), Bearbeiten/Löschen/"doppelt?" in Übertragen und Kalender, "Arbeitstag starten" optional (6.36).

## Offen
1. Bestätigung, dass es nach 6.32.2+ wieder flüssig läuft. Wenn nicht: system_logs Kategorie "leistung" auf dem Mac auslesen (`SELECT * FROM system_logs ORDER BY id DESC`, DB-Kopie per `VACUUM INTO`, danach löschen – ISO).
2. Tempo-Kontostand: Konto ca. −106 h (Okt −69 h, Jun −23 h, v.a. leere Tage), Sollzeiten Mo 4/Di 8/Mi 4/Do 8/Fr 8, Tempo rechnet 6,4 h/Tag. Frage an den Nutzer nach Anfangssaldo ist unbeantwortet.
3. Angebotene ADHS-Funktionen (wartet auf Ja): Zeitgefühl-Check nach 90/150 min + "du warst 25 min weg – abziehen?", "Unterbrechung"-Knopf mit automatischer Rückkehr zum vorigen Ticket, ein einzelnes "Als Nächstes" auf Heute, "Jetzt vermutlich" im kleinen Tracker-Fenster.
4. Angebotener Installer für weitere Macs (Node-Runtime, Electron-App mit Loader, DB-Export/Import, Keychain für Tokens, make-installer.sh) – noch nicht bestätigt.
5. Ungetestet: bereits gepushte Einträge auf neues Tempo-Ticket umhängen; Electron-Änderungen (mainInBrowser, Tray-Einträge, missingTick-Text) nur mit `node -c` geprüft; Stift/Löschen bei schon synchronisierten Einträgen im Browser.
6. Notiz: "Server vX gestartet" wurde zweimal geloggt, nicht untersucht.
7. Ideen (im Konzeptdokument): reine Browser-Nutzung mit Client-Stufen, mobile PWA, Claude/MCP, Go-Server mit zp-setup, Firefox-Signierung, ein einziger "Zeit buchen"-Dialog, gemeinsame Tagesliste für Heute/Nachtragen/Übertragen, SSH-Tracking für Linux/Windows.

## Konten-Fakten
Sommerurlaub muss über den Assistenten-Vorschlag "tempo:HAP-713" übernommen werden; Urlaub 12.–16.10. fehlt in Tempo. Vier alte ISMS-Buchungen vom 06.10. wurden ohne Ticket auf INTERNAL-4435 gepusht und stehen jetzt als "Ticket wählen".
