# Betrieb: Test, Umzug auf den Server, Cluster (Stand 09.11.2026)

## 1. Cluster-Fähigkeit – ehrlich
**Zum Testen und im Pilot: eine Replika.** Mehrere Replikas laufen im Prinzip, aber mit Einschränkungen:

| Thema | Stand |
|---|---|
| Sitzungen, Anmelde-Vorgänge (OIDC), Freigaben, Sperren | in der Datenbank → mit mehreren Pods in Ordnung |
| Migration beim Start | gesichert: eine Datenbank-Sperre lässt nur einen Pod migrieren, die anderen warten (neu) |
| Doppelbuchung Plane/Tempo | gesichert: Lauf je Person unter Datenbank-Sperre, Ansprüche (`pending:`) nur nach 10 min freigegeben (neu) |
| „Datenbank neuer als Programm“ | Programm startet nicht (neu) – schützt vor Start eines alten Images nach einem Update |
| Anmelde-Sperren, Anfragebegrenzung, Plane-/Jira-Bremse je Person | **im Speicher je Pod** → bei N Pods N-fach großzügiger; Plane-Limit (45/min je Person) kann dann überschritten werden. **Offen:** gemeinsamer Zähler in der Datenbank, bis dahin 1 Replika oder Router-Affinität |
| Hintergrundläufe (Selbsttest, Wächter, LDAP-Nachprüfung, Tempo-Zeitplan) | laufen je Pod, sind idempotent bzw. gesperrt; erzeugen aber N-fache Last |
Fazit: Hochverfügbarkeit über 2+ Pods erst nach dem gemeinsamen Zähler; für Ausfallschutz reicht im Pilot ein schneller Neustart (Probes, 1 Replika).

## 2. Testbetrieb mit eigener Datenbank
- Jede PostgreSQL-Datenbank genügt (lokal auf dem Mac, Container, VM): `DATABASE_URL=postgres://benutzer:passwort@host:5432/zeitprotokoll_test?sslmode=disable` (lokal), im Betrieb `sslmode=verify-full`.
- **Schlüssel von Anfang an per `ZP_SECRET_KEY` setzen** (mindestens 32 Zeichen, auch im Test) und aufbewahren. Ohne die Variable legt der Server im Test eine Schlüsseldatei `data/secret.key` an; deren Schlüssel unterscheidet sich vom späteren `ZP_SECRET_KEY`, und die verschlüsselten Tokens (Plane, Jira, LDAP-Dienstkonto, OIDC-Secret) wären nach dem Umzug nicht lesbar (Personen müssten Tokens neu eintragen).
- Start: Migrationen laufen automatisch (`server migrate` zeigt den Stand, `server check` prüft alles).

## 3. Umzug Test → Server (Migrationspfad)
1. Test-Server stoppen (keine Schreibzugriffe während der Sicherung).
2. Sichern: `pg_dump -Fc -d <testdatenbank> -f zeitprotokoll.dump`
3. Auf dem Zielserver leere Datenbank und eigenen Benutzer anlegen, dann: `pg_restore --no-owner --role=zeitprotokoll -d <zieldatenbank> zeitprotokoll.dump`
4. Auf dem Ziel **denselben `ZP_SECRET_KEY`** und die neue `DATABASE_URL` setzen, `ZP_PUBLIC_URL` auf die neue Adresse.
5. Starten: fehlende Migrationen werden nachgezogen. Danach `server check` und `server status`.
6. Anpassen, was an der Adresse hängt: OIDC-Rücksprung im Anbieter, Plane-/Jira-Adressen bleiben unverändert (Tokens gelten weiter), Sitzungen bleiben gültig (neue Anmeldung ist trotzdem sinnvoll).
7. Test-Konten entfernen (`server user remove …`), Notfallzugang anlegen (`server breakglass create`).
Ohne gleichen Schlüssel: Tokens neu eintragen (`server check --force` löscht unlesbare).

## 4. Versionen und Rückweg
- Migrationen gehen **nur vorwärts**. Vor jedem Update: Datenbank sichern (`pg_dump`). Rückweg = Dump zurückspielen und das alte Programm starten.
- Ein älteres Programm startet auf einer neueren Datenbank nicht (Meldung nennt die unbekannte Migration).
- Migrationsdateien, die schon auf einer echten Datenbank gelaufen sind, werden nie mehr geändert, nur durch neue ergänzt. (Bisher unbetroffen: Migrationen ab 010 liefen noch nirgends.)

## 5. Offen
- Gemeinsame Zähler in der Datenbank (Anmelde-Sperre, Anfragebegrenzung, Plane/Jira-Bremse) für echten Mehr-Pod-Betrieb.
- Befehl `server rekey` (alle verschlüsselten Werte von altem auf neuen Schlüssel umstellen), damit auch ein Test mit Schlüsseldatei verlustfrei auf `ZP_SECRET_KEY` umziehen kann.
- Getrennte Datenbank-Rollen (Betrieb/Migration), regelmäßige Sicherung, Wiederherstellungstest.
