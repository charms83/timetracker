# Zeitprotokoll – Browser-Add-on

Startet den normalen Timer von Zeitprotokoll mit einem Klick auf dem Jira- oder Plane-Ticket, das Du gerade offen hast. Der Timer läuft danach in der App bzw. am Team-Server, bis Du ihn stoppst. Das Add-on misst nie, wie lange eine Seite offen ist, und zeichnet nichts vom Browsen auf.

Funktioniert mit Firefox 128+ und Chromium-Browsern (Chrome, Edge, Brave, Arc, Vivaldi).

## Was es kann
- Popup: aktueller Status (Läuft / Pause / Kein Timer), Pause, Weiter, Stopp, "Tracken" und "+ To-do heute" für das offene Ticket.
- Kleine Pille unten rechts auf Jira/Plane-Tickets: "⏱ KEY tracken", beim laufenden Timer "● KEY läuft 0:42" (Hover: Stopp).
- Symbol-Badge: "●" = Timer läuft, "II" = Pause.
- Server-Adresse und optional Geräte-Token: lokale Mac-App (`http://127.0.0.1:3747`) oder Team-Server (`https://zeit.firma.example`).

## Berechtigungen und Warum
| Berechtigung | Grund |
|---|---|
| `http://127.0.0.1:3747/*` | Gespräch mit der lokalen Mac-App (Standard). |
| optional `http(s)://*/*` | Team-Server-Adresse, die Du im Popup einträgst (nur nach Freigabe). |
| `https://jira.example.com/*`, `https://plane.example.com/*` | Ticket-Nummer und Titel auf diesen Seiten lesen; Pille anzeigen. |
| `storage` | Pairing-Token und Server-Adresse lokal speichern. |
| `alarms` | Einmal pro Minute den Status für das Badge abfragen. |

Bewusst nicht dabei: `tabs`, `history`, `webRequest`, `<all_urls>` als feste Pflicht.

## Bauen
```bash
./build.sh
```
Ergebnis in `dist/`: `firefox/`, `zeitprotokoll-firefox.xpi` (unsigniert), `chromium/`, `zeitprotokoll-chromium.zip`. Tests: `node test/parse.test.js`.

## Installieren
**Firefox:** signierte `.xpi` öffnen (Signieren: `scripts/sign-firefox.sh`, Mozilla-Konto, Developer Hub, API-Schlüssel). Zum Testen: `about:debugging` -> "Dieser Firefox" -> "Temporäres Add-on laden" -> `dist/firefox/manifest.json`. Beim Installieren die Zugriffe auf die drei Hosts bestätigen.

**Chromium:** `chrome://extensions` -> Entwicklermodus an -> "Entpackt laden" -> Ordner `dist/chromium`.

## Verbinden
1. Zeitprotokoll starten (Mac-App lokal oder Team-Server im Browser).
2. Add-on-Symbol anklicken.
3. Bei Bedarf Server-Adresse setzen (Team: `https://…`), speichern.
4. Entweder **Verbinden** (Pairing mit Bestätigung in der App) **oder** Geräte-Token aus Einstellungen › Erkennung einfügen und speichern.
5. Fertig. Trennen geht im Popup unter „Verbindung“.

## Datenschutz
Es gibt keine Analytics und keinen Fremdcode. Daten gehen nur an die von Dir gesetzte Adresse (Standard: `127.0.0.1`). Gesendet wird nur, was Du auslöst: Ticket-Nummer und Titel beim Tracken bzw. To-do. Gespeichert werden Pairing-/Geräte-Token und die Server-Adresse (`storage.local`).
