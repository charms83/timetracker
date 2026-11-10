-- Getrennte PostgreSQL-Rollen für Zeitprotokoll (manuell als Superuser / DB-Admin ausführen).
-- Nicht Teil der App-Migrationen: Betrieb legt Rollen und Passwörter selbst fest.
--
-- zeitprotokoll_owner  – besitzt Schema/Objekte, führt Migrationen aus (server migrate / Start)
-- zeitprotokoll        – Laufzeit: DML + SELECT, keine DDL
--
-- Vorher: leere Datenbank anlegen, z. B.
--   CREATE DATABASE zeitprotokoll OWNER zeitprotokoll_owner;
-- Dann dieses Skript in der Datenbank ausführen und Passwörter setzen.
-- DATABASE_URL für den Server mit Benutzer „zeitprotokoll“; Migrationen mit „zeitprotokoll_owner“
-- (gleiche URL mit anderem Benutzer, oder vor dem ersten Start einmalig als Owner migrieren und
-- danach DEFAULT PRIVILEGES / GRANT wie unten).

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'zeitprotokoll_owner') THEN
    CREATE ROLE zeitprotokoll_owner LOGIN PASSWORD 'CHANGEME_OWNER';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'zeitprotokoll') THEN
    CREATE ROLE zeitprotokoll LOGIN PASSWORD 'CHANGEME_APP';
  END IF;
END $$;

-- Schema-Rechte
GRANT CONNECT ON DATABASE zeitprotokoll TO zeitprotokoll_owner, zeitprotokoll;
GRANT USAGE, CREATE ON SCHEMA public TO zeitprotokoll_owner;
GRANT USAGE ON SCHEMA public TO zeitprotokoll;

-- Bestehende Objekte (nach Migrationen als Owner erneut ausführen)
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO zeitprotokoll;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO zeitprotokoll;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO zeitprotokoll;

ALTER DEFAULT PRIVILEGES FOR ROLE zeitprotokoll_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO zeitprotokoll;
ALTER DEFAULT PRIVILEGES FOR ROLE zeitprotokoll_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO zeitprotokoll;
ALTER DEFAULT PRIVILEGES FOR ROLE zeitprotokoll_owner IN SCHEMA public
  GRANT EXECUTE ON FUNCTIONS TO zeitprotokoll;

-- Empfehlung: Search-Path und Ownership
--   ALTER DATABASE zeitprotokoll OWNER TO zeitprotokoll_owner;
--   ALTER SCHEMA public OWNER TO zeitprotokoll_owner;
-- Passwörter sofort ändern (CHANGEME_*), sslmode=verify-full in DATABASE_URL.
