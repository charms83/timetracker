# Mac-Client am Team-Server (Go)

Der Desktop-Client (`desktop/`) kann im Modus **team** mit dem Go-Server arbeiten, statt den lokalen Node-Server zu starten.

## Einrichtung

In `~/.zeitprotokoll/desktop.json` (oder per IPC `cfg:setTeam`):

```json
{
  "mode": "team",
  "serverUrl": "https://zeit.firma.example",
  "allowHttpTeam": false,
  "serverCertPin": ""
}
```

1. `mode` auf `team`, `serverUrl` auf die öffentliche **https**-Adresse setzen.
2. Optional `serverCertPin`: SHA-256 des Server-Zertifikats (64 Hex) – Pinning gegen MITM.
3. Client starten → **Zeitprotokoll öffnen** (Server-UI im App-Fenster).
4. **Einmal anmelden** – Sitzung liegt auf dem Server (`__Host-zp_session`, HttpOnly + Secure).

**Kein Geräte-Token nötig.** Passwort/OIDC bleiben auf Server/IdP.

Zurück zum Einzelplatz: `"mode": "local"`.  
Dev: `allowHttpTeam: true` nur für `http://localhost` / `127.0.0.1`.

## Verbindungs-Sicherheit

| Schutz | Wirkung |
|---|---|
| HTTPS Pflicht | Team-URL ohne `https://` wird abgelehnt |
| TLS-Prüfung | Ungültige/abgelaufene Zertifikate werden nie akzeptiert |
| Optional Cert-Pin | Nur exakt dieses Server-Zertifikat für `serverUrl` |
| Kein HTTP-Downgrade | Klartext-Requests im Team-Modus blockiert |
| Server-Sitzung | Cookie `__Host-zp_session` (Secure, HttpOnly, Path=/); Hash mit `ZP_SECRET_KEY` |
| HSTS | Server sendet `Strict-Transport-Security` bei https-`ZP_PUBLIC_URL` |
| Betrieb | `ZP_PUBLIC_URL=https://…` ist Pflicht |

## Was speichert wer?

| Was | Wo |
|---|---|
| Sitzung | **Server** (`sessions`, HMAC); Client nur HttpOnly-Cookie |
| Passwort / OIDC | **Server** / IdP |
| Geräte-Token | optional (Add-on/curl); Server nur HMAC |

## Noch offen

- Tray-UI für Server-URL / Cert-Pin
- Timer im Mini über dieselbe Session
- Add-on auf Server-URL
