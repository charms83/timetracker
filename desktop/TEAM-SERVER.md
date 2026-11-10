# Mac-Client am Team-Server (Go)

Der Desktop-Client (`desktop/`) kann im Modus **team** mit dem Go-Server arbeiten, statt den lokalen Node-Server zu starten.

## Einrichtung

In `~/.zeitprotokoll/desktop.json` (oder per IPC `cfg:setTeam`):

```json
{
  "mode": "team",
  "serverUrl": "https://zeit.firma.example",
  "deviceToken": "zpdev_…",
  "allowHttpTeam": false
}
```

1. Im Browser am Team-Server anmelden → **Einstellungen › Erkennung** → Geräte-Token (`kind: mac`) erzeugen.
2. Token **einmal** kopieren und in `deviceToken` speichern (Klartext nur lokal, nie ins Log).
3. `mode` auf `team`, `serverUrl` auf die öffentliche **https**-Adresse setzen.
4. Client neu starten. Hauptfenster öffnet die Server-Oberfläche; Anwesenheit/Meetings gehen als `POST /api/client/hints`.

Zurück zum Einzelplatz: `"mode": "local"` (Node auf Port 3747 wie bisher).

Dev: `allowHttpTeam: true` erlaubt `http://localhost` bzw. `http://127.0.0.1` – sonst wird Team-Modus mit HTTP abgelehnt.

## Sicherheit

- Geräte-Token: nie Admin-, nie Session-Ersatz für fremde Personen; widerrufbar in der UI; Server lehnt `role=admin`/`breakglass` ab.
- Header nur an `/api/*` (nicht an JS/CSS/Fonts) – weniger Log-/HAR-Exposition.
- `serverUrl` muss `https://` sein (Ausnahme localhost mit `allowHttpTeam`).
- `desktop.json` wird mit Modus `0600` geschrieben (Token nicht weltlesbar).
- `cfg:get` liefert den Token nur gekürzt (`zpdev_ab…`).
- Token-Format: `zpdev_` + Base64url, Länge begrenzt.
- Header: `X-ZP-Device-Token` bzw. `Authorization: Bearer zpdev_…`.
- Electron-Renderer gegen Remote: Schreibzugriffe mit `Origin: null` lehnt der Go-Server ab – API-Aufrufe laufen im Main-Prozess (`fetch` ohne Origin).
- Server: Ingest mit Geräte-Token max. 60/min je Gerät, max. 50 Hinweise/Aufruf, Payload ≤ 4 KiB; Body ohnehin ≤ 64 KiB.
- Meetings/SSH/Kalender bleiben lokal erkannt; der Server speichert nur Hinweise, keine stillen Buchungen.

## Noch offen

- UI im Schnellfenster zum Setzen von URL/Token
- Timer/Pause im Team-Modus über Session-Cookie oder erweiterte Device-Routen
- Add-on auf Server-URL umstellen
- Token im macOS-Schlüsselbund statt Klartext in `desktop.json`
