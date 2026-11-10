# API für Abrechnungsprogramme

Die offene API ist für **Lohn-/Abrechnungsprogramme** und ähnliche Integrationen – **nicht** für den Mitarbeiter-Alltag (der läuft per Browser-Login).

Geräte-Token (`zpdev_…`) bleiben nur für Mac-Client/Add-on (Hinweise). Abrechnung nutzt eigene Tokens (`zpint_…`).

## Token anlegen (Administrator)

1. `/admin` → Tab **Abrechnung-API**
2. Bezeichnung setzen → Token anlegen → Klartext **einmal** kopieren
3. Im Abrechnungsprogramm als Bearer-Token hinterlegen
4. Bei Verlust: widerrufen und neu anlegen

Server speichert nur HMAC. Scope derzeit: `payroll`.

## Authentifizierung

```http
Authorization: Bearer zpint_…
```

Kein `Origin`-Header (curl/Skript). HTTPS in Betrieb.

## Endpunkte (Lohn)

### Monatssummen

```bash
curl -sS -H "Authorization: Bearer $ZPINT" \
  "$ZP_URL/api/integrations/payroll?month=2026-11"
```

Standard: nur Personen mit Status **approved** oder **closed**.  
Vollständigkeit prüfen: `?month=2026-11&all=1`.

Antwort je Person u. a.: `id`, `name`, `email`, `status`, `soll_h`, `ist_h`, `saldo_h`, `konto_h`.

### Einzelbuchungen einer Person

```bash
curl -sS -H "Authorization: Bearer $ZPINT" \
  "$ZP_URL/api/integrations/payroll/entries?month=2026-11&user_id=42"
```

Wenn der Monat nicht freigegeben ist → `409` mit `code: month_not_ready` (außer `?all=1`).

## Was noch fehlt (bewusst offen)

- Kunden-Export (Scope `customers`)
- „exportiert“-Markierung / Sperre (Spez §16.7 – Format mit Buchhaltung klären)
- feste CSV/DATEV-Dateiformate

## Admin-API

Token-Verwaltung nur per Admin-Sitzung: `GET/POST/DELETE /api/admin/integrations`.
