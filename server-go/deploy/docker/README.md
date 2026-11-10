# Docker Compose

Einfachster Weg, wenn PostgreSQL und Server zusammen in Containern laufen sollen – **ohne Ansible**.

```bash
cd server-go/deploy/docker
cp .env.example .env
# Passwort + ZP_SECRET_KEY setzen (≥ 32 Zeichen)
docker compose up -d --build
curl -fsS http://127.0.0.1:8080/healthz
docker compose logs -f app   # Setup-Token für /setup
```

Stoppen: `docker compose down` · Daten behalten Volume `zp_pgdata`.  
Mit Daten löschen: `docker compose down -v` (Vorsicht).

Sicherheit: Standard-Bind nur `127.0.0.1`, Container read-only, `no-new-privileges`, Capabilities leer. Für den Betrieb TLS davor (Caddy/nginx) und `ZP_PUBLIC_URL=https://…`.
