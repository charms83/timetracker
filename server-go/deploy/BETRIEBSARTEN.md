# Betriebsarten – ohne Ansible

Zeitprotokoll ist ein Tracking-System: absichtlich **kein** Ansible/Semaphore. Drei Wege, ein Programm.

| Weg | Wann | Aufwand | Sicherheit |
|---|---|---|---|
| **1. Binary + systemd** (empfohlen für eine VM) | Eine Linux-Maschine, Postgres schon da oder lokal | gering | am besten: kleine Angriffsfläche, gehärtete Unit, Secrets in `/etc` |
| **2. Docker Compose** | Schnell alles inkl. Postgres, Test/Pilot | sehr gering | gut, wenn Bind `127.0.0.1` + TLS-Proxy; Secrets nur in `.env` |
| **3. OpenShift** | Cluster schon vorhanden (Firma) | mittel | gut im Cluster; Image UBI, ohne Root |

## Empfehlung

- **Am einfachsten und sichersten für den Alltag:** Binary + systemd (`install/zp-install --profile linux-server` oder stilles `deploy/linux/install.sh`).
- **Am schnellsten zum Ausprobieren:** Docker Compose (`deploy/docker/` oder `zp-install --profile docker`).
- **OpenShift:** nur wenn ihr den Cluster ohnehin betreibt (`deploy/openshift/`).

Alle drei nutzen dieselbe Go-Binary und dieselbe PostgreSQL-Datenbank. Umzug: `pg_dump` + gleicher `ZP_SECRET_KEY` (siehe `docs/Betrieb-Migration.md`).

## Einstieg Installer

```bash
cd server-go
sudo ./install/zp-install          # Menü: Binary / Docker / OpenShift
./install/zp-install --profile docker
sudo ./install/zp-install --profile linux-server
./install/zp-install --profile openshift
```
