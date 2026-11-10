-- LDAP/AD-Konten: Kontoart „ldap“ (Anmeldung gegen das Verzeichnis, kein Passwort in der Datenbank)
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kind_check;
ALTER TABLE users ADD CONSTRAINT users_kind_check CHECK (kind IN ('oidc','local','ldap'));
