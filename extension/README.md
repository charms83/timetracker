# Zeitprotokoll – Browser-Add-on

Startet den normalen Timer der lokalen Mac-App "Zeitprotokoll" mit einem Klick auf dem Jira- oder Plane-Ticket, das Du gerade offen hast. Der Timer läuft danach in der App, bis Du ihn stoppst. Das Add-on misst nie, wie lange eine Seite offen ist, und zeichnet nichts vom Browsen auf.

Funktioniert mit Firefox 128+ und Chromium-Browsern (Chrome, Edge, Brave, Arc, Vivaldi).

## Was es kann
- Popup: aktueller Status (Läuft / Pause / Kein Timer), Pause, Weiter, Stopp, "Tracken" und "+ To-do heute" für das offene Ticket.
- Kleine Pille unten rechts auf Jira/Plane-Tickets: "⏱ KEY tracken", beim laufenden Timer "● KEY läuft 0:42" (Hover: Stopp).
- Symbol-Badge: "●" = Timer läuft, "II" = Pause.

## Berechtigungen und Warum
| Berechtigung | Grund |
|---|---|
| `http://127.0.0.1:3747/*` | Gespräch mit der lokalen Mac-App (sonst nichts). |
| `https://jira.example.com/*`, `https://plane.example.com/*` | Ticket-Nummer und Titel auf diesen Seiten lesen; Pille anzeigen. |
| `storage` | Pairing-Token lokal speichern. |
| `alarms` | Einmal pro Minute den Status für das Badge abfragen. |

Bewusst nicht dabei: `tabs`, `history`, `webRequest`, `<all_urls>`.

## Bauen
```bash
./build.sh
```
Ergebnis in `dist/`: `firefox/`, `zeitprotokoll-firefox.xpi` (unsigniert), `chromium/`, `zeitprotokoll-chromium.zip`. Tests: `node test/parse.test.js`.

## Installieren
**Firefox:** signierte `.xpi` öffnen (Signieren: `scripts/sign-firefox.sh`, Mozilla-Konto, Developer Hub, API-Schlüssel). Zum Testen: `about:debugging` -> "Dieser Firefox" -> "Temporäres Add-on laden" -> `dist/firefox/manifest.json`. Beim Installieren die Zugriffe auf die drei Hosts bestätigen.

**Chromium:** `chrome://extensions` -> Entwicklermodus an -> "Entpackt laden" -> Ordner `dist/chromium`.

## Verbinden (Pairing)
1. Mac-App starten.
2. Add-on-Symbol anklicken -> "Mit Zeitprotokoll verbinden".
3. In der App "Add-on verbinden?" mit "Erlauben" bestätigen (Du hast 3 Minuten).
4. Fertig. Wird der Zugriff in der App widerrufen, fragt das Add-on automatisch neu.

## Datenschutz
Es gibt keine Analytics, keinen Fremdcode und keine externen Server. Daten gehen ausschließlich an `127.0.0.1` (Deine Mac-App). Gesendet wird nur, was Du auslöst: Ticket-Nummer und Titel beim Tracken bzw. To-do. Gespeichert wird nur das Pairing-Token (`storage.local`).
