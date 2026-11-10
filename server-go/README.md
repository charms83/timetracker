# server-go

Go-Server für die OpenShift-Version (Plan: `../docs/OpenShift-Server-Plan.md`). Läuft parallel zum Node-System.

## Lokal starten
```bash
brew services start postgresql@16   # einmalig
createdb zeitprotokoll              # einmalig
cd ~/Claude/zeitprotokoll/server-go && go mod tidy
export DATABASE_URL="postgres://localhost/zeitprotokoll?sslmode=disable"
ZP_ADDR=":8090" go run ./cmd/server   # 8080 ist bei Beispiel-Macs ggf. belegt
curl localhost:8090/healthz           # zweiter Tab
```

## Umgebungsvariablen
- `DATABASE_URL` (Pflicht), `ZP_ADDR` (`:8080`), `ZP_STATIC_DIR` (Pfad zu `frontend/dist`), `ZP_SECRET_KEY` (ab Phase 1c)

## Installation (ohne Ansible)
Überblick: `deploy/BETRIEBSARTEN.md`

| Weg | Befehl |
|---|---|
| **Binary + systemd** (empfohlen) | `sudo ./install/zp-install --profile linux-server` |
| **Docker Compose** | `cd deploy/docker && cp .env.example .env && docker compose up -d --build` |
| **OpenShift** | `deploy/openshift/` – siehe `deploy/README.md` |

Paket: `scripts/package.sh` → `dist/…tar.gz`

## Stand
Aktuell: `docs/Stand-und-Plan.md`. Migrationen, Mehrbenutzer, Plane/Jira/Tempo, Freigabe; Installer modular.
