# Modularer Server-Installer

Auswahldialoge in der Shell – **kein** Ansible (Tracking-System, absichtlich schlank).  
Betriebsarten: [../deploy/BETRIEBSARTEN.md](../deploy/BETRIEBSARTEN.md)

## Einstieg

```bash
cd server-go
chmod +x install/zp-install
./install/zp-install --list
sudo ./install/zp-install                          # Menü: Binary | Docker | OpenShift
sudo ./install/zp-install --profile linux-server   # Binary+systemd (empfohlen)
./install/zp-install --profile docker              # Compose inkl. Postgres
sudo ./install/zp-install --profile upgrade        # Update
./install/zp-install --profile openshift           # Cluster-Hilfe
```

Simulation ohne Root:

```bash
./install/zp-install --dry-run --yes --root /tmp/zp-root --profile linux-server
```

## Module

| Modul | Aufgabe |
|---|---|
| `check` | Voraussetzungen (OS, Arch, psql, Binary, web) |
| `postgres` | Rolle + Datenbank anlegen |
| `binary` | Programm nach `/usr/local/bin` (Paket oder `go build`) |
| `web` | Oberfläche nach `/usr/share/zeitprotokoll/web` |
| `config` | `/etc/zeitprotokoll/zeitprotokoll.env` (Schlüssel, URL) |
| `systemd` | Unit + enable/start |
| `docker` | Docker Compose (App + Postgres) |
| `verify` | healthz / `server check` |
| `openshift` | Manifest-Check, optional `oc apply -k` |
| `uninstall` | Dienst + Binary weg (`--purge` auch Config) |

Profile liegen unter `profiles/*.conf` (eine Modul-ID je Zeile).

## Verhältnis zum stillen Paket-Installer

- `deploy/linux/install.sh` – kurz, nicht interaktiv, für das fertige `tar.gz`
- `install/zp-install` – modular, Dialoge, Profile, Dry-Run, Testroot

`scripts/package.sh` packt beides ins Linux-Paket.

## Paket bauen

```bash
cd server-go
scripts/build-web.sh    # optional, aber empfohlen
scripts/package.sh 20261110
# → dist/zeitprotokoll-server-….tar.gz
```
