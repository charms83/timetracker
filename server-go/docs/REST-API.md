# REST-API-Zugang

Der Team-Server spricht JSON unter `/api/…`. Zugang für Skripte und Integrationen: **persönliches API-Token** (kein Passwort, kein Admin).

## Token anlegen

1. Im Browser anmelden → **Einstellungen › Erkennung** (Geräte-/API-Token)
2. Art **API / REST** wählen, Token anlegen, Klartext **einmal** kopieren
3. Widerruf jederzeit in derselben Liste

Server speichert nur einen HMAC-Hash. Token gelten nur für Konten mit Zeiterfassung – nie für Admin/`/api/admin/*`.

## Authentifizierung

```http
Authorization: Bearer zpdev_…
```

oder

```http
X-ZP-Device-Token: zpdev_…
```

Browser-UI nutzt alternativ das Sitzungs-Cookie (`__Host-zp_session` / `zp_session`). Curl/Skripte: **kein** `Origin`-Header senden (CSRF-Schutz).

## Schnelltest

```bash
export ZP_URL=https://zeit.firma.example
export ZP_TOKEN=zpdev_…

curl -sS -H "Authorization: Bearer $ZP_TOKEN" "$ZP_URL/api/me" | jq .
curl -sS -H "Authorization: Bearer $ZP_TOKEN" "$ZP_URL/api/health"
```

## Typische Aufrufe (eigene Daten)

| Methode | Pfad | Zweck |
|---|---|---|
| GET | `/api/me` | Wer bin ich |
| GET | `/api/time-entries?from=2026-11-01&to=2026-11-30` | Buchungen |
| POST | `/api/time-entries` | Buchung anlegen |
| GET | `/api/projects` | Projekte |
| GET | `/api/assistant/tasks` | Assistent-Aufgaben |
| POST | `/api/client/hints` | Client-Hinweise (auch ohne Session) |

Alle `a.auth()`-Routen akzeptieren das Token. **Nicht** erreichbar per Token: `/api/admin/*`, Passwort ändern, Logout/Login.

## Grenzen

- Rate-Limit je IP; Body max. ~64 KiB
- HTTPS in Betrieb (`ZP_PUBLIC_URL=https://…`)
- Token wie ein Passwort behandeln; bei Verlust widerrufen und neu anlegen
