-- Plausibilitätsprüfung: Markierung (Codes, kommagetrennt) und Begründung bei bestätigter Buchung.
ALTER TABLE time_entries ADD COLUMN check_flags TEXT, ADD COLUMN check_note TEXT;
