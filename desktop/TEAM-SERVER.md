# Mac-Client am Team-Server (Go)

Der Desktop-Client (`desktop/`) kann im Modus **team** mit dem Go-Server arbeiten, statt den lokalen Node-Server zu starten.

## Einrichtung

In `~/.zeitprotokoll/desktop.json` (oder per IPC `cfg:setTeam`):

```json
{
  "mode": "team",
  "serverUrl": "https://zeit.firma.example",
  "allowHttpTeam": false
}
```

1. `mode` auf `team`, `serverUrl` auf die öffentliche **https**-Adresse setzen.
2. Client starten → **Zeitprotokoll öffnen** (öffnet die Server-Oberfläche im App-Fenster).
3. **Einmal anmelden** (LDAP/OIDC/lokal) – der Server legt eine Sitzung an (`zp_session`-Cookie, HttpOnly).
4. Fertig: Daten abrufen und buchen wie im Browser. Anwesenheit/Meetings gehen als `POST /api/client/hints` mit derselben Sitzung.

**Kein Geräte-Token nötig.** Die Anmeldung liegt auf dem Server; der Client speichert kein Passwort und kein Token für den Normalbetrieb.

Zurück zum Einzelplatz: `"mode": "local"`.

Dev: `allowHttpTeam: true` erlaubt `http://localhost` / `127.0.0.1`.

## Was speichert wer?

| Was | Wo |
|---|---|
| Sitzung (Login) | **Server** (Tabelle `sessions`, Hash); Client nur HttpOnly-Cookie in der Electron-Session |
| Passwort / OIDC | **Server** / IdP – nie im Client |
| Geräte-Token | **optional**, nur Add-on/curl/Skript ohne Browser-Login; Server nur HMAC |

## Optionales Geräte-Token

Nur für Browser-Add-on, curl oder Headless ohne Login-Fenster: unter **Einstellungen › Erkennung** anlegen. Der Mac-Client braucht das nicht, wenn man sich im Fenster anmeldet.

## Sicherheit

- Team-UI immer im Electron-Fenster (nicht Systembrowser), damit die Sitzung in der App bleibt.
- API aus dem Main-Prozess: `session.fetch` mit Cookie, ohne `Origin` (CSRF-Schutz).
- `serverUrl` nur `https://` (localhost-HTTP mit `allowHttpTeam`).
- Geräte-Token falls gesetzt: `safeStorage`, nicht in `desktop.json`.

## Noch offen

- Tray-UI zum Setzen der Server-URL
- Timer/Pause im Mini-Fenster über dieselbe Session
- Add-on auf Server-URL (dann optional Geräte-Token)
