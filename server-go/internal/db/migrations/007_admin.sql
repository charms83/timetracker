-- Benutzer sperren (Admin-Oberfläche / Befehl „user disable“); Logs nach Zeit/Kategorie durchsuchbar.
ALTER TABLE users ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX system_logs_cat_idx ON system_logs(category, id DESC);
