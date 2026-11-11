# Mac-Client am Team-Server (Go)

**Maßstab:** Das vollständige lokale Mac-System (Node + Mini + Tray + Erkennung) – Alltag (Timer, Meetings, Anwesenheit, Tempo/Plane) soll am Team-Server genauso wirken. Team/Personal/Admin sind Zusatz, ersetzen den lokalen Alltag nicht.

## Alltag (so arbeitet man damit)

1. In `~/.zeitprotokoll/desktop.json` nur setzen:
   ```json
   { "mode": "team", "serverUrl": "https://zeit.firma.example" }
   ```
2. App starten → **Zeitprotokoll öffnen**
3. Einmal anmelden (LDAP/OIDC/lokal) → fertig buchen, Team, Assistent wie im Browser

Kein Token kopieren, kein Zertifikat-Pin nötig. Die Sitzung liegt auf dem Server; der Client speichert kein Passwort.

Zurück zum Einzelplatz: `"mode": "local"`.

## Was sicher ist (ohne den Alltag zu stören)

| Maßnahme | Alltag |
|---|---|
| Nur `https://` | Normalfall; lokal-Dev: `allowHttpTeam: true` |
| Ungültige Zertifikate abgelehnt | Firmen-CA / System-Vertrauensspeicher funktioniert weiter |
| Optional `serverCertPin` | Nur wenn die IT Pinning will – sonst weglassen |
| Login-Sitzung auf dem Server | Einmal anmelden, 12 h Session, Cookie HttpOnly |
| OIDC | Weiterleitung zum IdP (https) bleibt erlaubt |
| Geräte-Token | Nur Add-on/curl – Mac-App braucht keins |

## Optional (härter)

```json
{
  "mode": "team",
  "serverUrl": "https://zeit.firma.example",
  "serverCertPin": "aabbcc…64 hex SHA-256…",
  "allowHttpTeam": false
}
```

`serverCertPin`: SHA-256 des Server-Zertifikats. Ohne Pin gilt die normale Zertifikatsprüfung (inkl. Firmen-CA).

## Server (Betrieb)

- `ZP_PUBLIC_URL=https://…` Pflicht
- Sitzung: `__Host-zp_session` + HMAC in der DB
- HSTS aktiv

## Mini / Tray (Stand)

- Schnellfenster (Mini) spricht den Team-Server über die Electron-Session an (gleiche Login-Sitzung wie das Hauptfenster) – kein extra Geräte-Token.
- Tray zeigt Modus/Server-Host und öffnet Einstellungen.
- Anwesenheit („am Mac“): mit Login-Sitzung `POST /presence` → Gaps/Heute/Tempo-Ausgang; nur Geräte-Token: Hint, Server legt `presence_min` an.
- Meetings/Kalender: Mac-App meldet Signale und Termine an den Server (`/meetings/*`); Mini fragt nach wie lokal; Browser zeigt Vorschläge/Einstellungen › Erkennung.
- SSH: Mac synct `~/.zeitprotokoll/ssh_sessions.log` an den Server; Browser-Ansicht und Nachtragen wie lokal.

## Noch offen

- Add-on auf Server-URL
- Server-URL im Tray editieren (heute nur Anzeige + Einstellungen öffnen)
