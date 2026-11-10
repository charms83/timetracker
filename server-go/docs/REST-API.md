# API für Abrechnungsprogramme

Die offene API ist für **Abrechnungsprogramme** (Projektstunden, optional Lohn) – **nicht** für den Mitarbeiter-Alltag (der läuft per Browser-Login).

Geräte-Token (`zpdev_…`) bleiben nur für Mac-Client/Add-on (Hinweise). Abrechnung nutzt eigene Tokens (`zpint_…`).

## Token anlegen (Administrator)

1. `/admin` → Tab **Abrechnung-API**
2. Bezeichnung setzen → Token anlegen → Klartext **einmal** kopieren
3. Im Abrechnungsprogramm als Bearer-Token hinterlegen
4. Bei Verlust: widerrufen und neu anlegen

Server speichert nur HMAC. Scopes: `projects` (Stunden auf Projekten/Tickets), `payroll` (Monatssummen je Person). Standard bei neuem Token: beide.

## Authentifizierung

```http
Authorization: Bearer zpint_…
```

Kein `Origin`-Header (curl/Skript). HTTPS in Betrieb.

## Endpunkte – Projektstunden

### Summen je Projekt

```bash
curl -sS -H "Authorization: Bearer $ZPINT" \
  "$ZP_URL/api/integrations/projects?month=2026-11"
```

Gruppierung nach Jira-Key (sonst Projektname). Felder u. a.: `project_key`, `project_name`, `seconds`, `hours`, `billable_seconds`, `billable_hours`, `entry_count`, `people`.

Standard: nur Personen mit Status **approved** oder **closed**. Abwesenheiten zählen nicht.  
Vollständigkeit: `?month=2026-11&all=1`.

### Einzelbuchungen (Projekt / Ticket / Kommentar)

```bash
curl -sS -H "Authorization: Bearer $ZPINT" \
  "$ZP_URL/api/integrations/projects/entries?month=2026-11"
```

Optional: `project_key=…`, `user_id=…`, `all=1`.

Je Zeile u. a.: Datum, Person, `project_key`/`project_name`, `ticket`/`task_name`, Kommentar, `hours`, `billable`, `tempo_account`.

## Endpunkte – Lohn (optional)

### Monatssummen je Person

```bash
curl -sS -H "Authorization: Bearer $ZPINT" \
  "$ZP_URL/api/integrations/payroll?month=2026-11"
```

Antwort je Person u. a.: `id`, `name`, `email`, `status`, `soll_h`, `ist_h`, `saldo_h`, `konto_h`.

### Einzelbuchungen einer Person

```bash
curl -sS -H "Authorization: Bearer $ZPINT" \
  "$ZP_URL/api/integrations/payroll/entries?month=2026-11&user_id=42"
```

Wenn der Monat nicht freigegeben ist → `409` mit `code: month_not_ready` (außer `?all=1`).

## Was noch fehlt (bewusst offen)

- „exportiert“-Markierung / Sperre (Spez §16.7 – Format mit Buchhaltung klären)
- feste CSV/DATEV-Dateiformate
- eigene Kundenstammdaten (aktuell Projekt = Abrechnungseinheit)

## Admin-API

Token-Verwaltung nur per Admin-Sitzung: `GET/POST/DELETE /api/admin/integrations`.
