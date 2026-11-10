# Mac-Client am Team-Server (Go)

Der Desktop-Client (`desktop/`) kann im Modus **team** mit dem Go-Server arbeiten, statt den lokalen Node-Server zu starten.

## Einrichtung

In `~/.zeitprotokoll/desktop.json` (oder per IPC `cfg:setTeam`) nur Modus und URL:

```json
{
  "mode": "team",
  "serverUrl": "https://zeit.firma.example",
  "allowHttpTeam": false
}
```

Das **Geräte-Token** gehört **nicht** in `desktop.json`. Es liegt verschlüsselt in
`~/.zeitprotokoll/device-token.bin` (Electron `safeStorage` → macOS-Schlüsselbund / Windows DPAPI).
Auf dem Server steht nur ein **HMAC-Hash** (mit `ZP_SECRET_KEY`), nie der Klartext.

1. Im Browser am Team-Server anmelden → **Einstellungen › Erkennung** → Geräte-Token (`kind: mac`) erzeugen.
2. Token **einmal** kopieren und per `cfg:setTeam({ deviceToken: "zpdev_…" })` setzen (oder künftige Tray-UI).
3. `mode` auf `team`, `serverUrl` auf die öffentliche **https**-Adresse setzen.
4. Client neu starten. Hauptfenster öffnet die Server-Oberfläche; Anwesenheit/Meetings gehen als `POST /api/client/hints`.

Alte Installationen: stand der Token noch in `desktop.json`, wandert er beim Start automatisch in den Credential-Store und wird aus der JSON entfernt.

Zurück zum Einzelplatz: `"mode": "local"` (Node auf Port 3747 wie bisher).

Dev: `allowHttpTeam: true` erlaubt `http://localhost` bzw. `http://127.0.0.1` – sonst wird Team-Modus mit HTTP abgelehnt.

## Sicherheit

- **Server:** Klartext nur einmal in der Create-Antwort; DB speichert `v1:`+HMAC-SHA256(`ZP_SECRET_KEY`). Alte SHA-256-Hashes werden bei Nutzung einmalig umgestellt. Nie Admin/Breakglass.
- **Client:** Token nur im System-Credential-Store (`safeStorage`), nicht in `desktop.json`; `cfg:get` liefert höchstens `zpdev_ab…`.
- Header nur an `/api/*` (nicht an JS/CSS/Fonts).
- `serverUrl` muss `https://` sein (Ausnahme localhost mit `allowHttpTeam`).
- `desktop.json` Mode `0600`; Token-Datei Mode `0600`.
- Header: `X-ZP-Device-Token` bzw. `Authorization: Bearer zpdev_…`.
- Electron-Renderer gegen Remote: Schreibzugriffe mit `Origin: null` lehnt der Go-Server ab – API-Aufrufe laufen im Main-Prozess (`fetch` ohne Origin).
- Server: Ingest mit Geräte-Token max. 60/min je Gerät, max. 50 Hinweise/Aufruf, Payload ≤ 4 KiB; Body ohnehin ≤ 64 KiB.
- Meetings/SSH/Kalender bleiben lokal erkannt; der Server speichert nur Hinweise, keine stillen Buchungen.

## Noch offen

- UI im Schnellfenster zum Setzen von URL/Token
- Timer/Pause im Team-Modus über Session-Cookie oder erweiterte Device-Routen
- Add-on auf Server-URL umstellen
