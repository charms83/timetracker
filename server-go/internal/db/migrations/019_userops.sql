-- Indizes für die Übersichten Teamleiter/Personal und den eigenen Änderungsverlauf (nur lesen, keine Datenänderung).
-- entry_audit hatte nur einen Index auf row_id; /api/audit filtert je Person.
CREATE INDEX IF NOT EXISTS entry_audit_user_idx ON entry_audit(user_id, id);
-- Monatsstatus aller Personen eines Monats (Personal-Übersicht)
CREATE INDEX IF NOT EXISTS month_approvals_month_idx ON month_approvals(month, status);
