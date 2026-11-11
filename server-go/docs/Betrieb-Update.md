# Server aktualisieren

Es gibt **kein** In-App-Upload von Programmdateien (Sicherheit). Updates laufen über Installer oder Container-Image. Die Admin-Oberfläche `/admin` → Tab **Update** zeigt Version und Schritte.

## Vor jedem Update

1. Datenbank sichern: `pg_dump …`
2. `ZP_SECRET_KEY` und `DATABASE_URL` unverändert lassen (sonst Tokens unlesbar)
3. Kurzes Wartungsfenster einplanen (Dienstneustart)

## Linux (systemd)

```bash
# Paket bauen (oder aus Ablage holen)
server-go/scripts/package.sh 20261110
# Prüfen
shasum -a 256 -c dist/zeitprotokoll-server-20261110.tar.gz.sha256

# Auf dem Server
tar xzf zeitprotokoll-server-20261110.tar.gz
cd zeitprotokoll-server-20261110
sudo ./install.sh --upgrade
```

Das Skript sichert Binary (und Web) unter `/var/lib/zeitprotokoll/backup/`, tauscht Programm + Oberfläche, startet den Dienst neu. Migrationen laufen beim Start.

Prüfen:

```bash
set -a; . /etc/zeitprotokoll/zeitprotokoll.env; set +a
zeitprotokoll-server version
zeitprotokoll-server status
zeitprotokoll-server check
```

Rückweg: altes Binary aus dem Backup zurückkopieren und Dienst starten; DB nur per Dump zurück.

## OpenShift / Container

1. Image mit neuer Version bauen und pushen
2. Deployment-Tag aktualisieren → Rollout
3. Bei Fehler „Datenbank neuer als Programm“: Image ist zu alt für die Migrationen – neueres Image nehmen

## Was sich nicht ändert

- Konfiguration `/etc/zeitprotokoll/zeitprotokoll.env` (Linux)
- Datenbank-Inhalte (nur vorwärts Migrationen)
- Persönliche Plane-/Jira-Schlüssel (solange derselbe `ZP_SECRET_KEY`)
