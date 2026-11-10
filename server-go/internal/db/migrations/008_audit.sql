-- Änderungsprotokoll für Zeiteinträge: wird von der Datenbank selbst geschrieben (nicht von der Anwendung umgehbar),
-- mit Hash-Kette über alle Einträge. audit_log (Anmeldung, Zugangsdaten, Einrichtung) ist gegen Ändern und Löschen geschützt.

CREATE FUNCTION time_entries_audit() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    prev TEXT; b JSONB; a JSONB; act TEXT; uid BIGINT; rid BIGINT; h TEXT; ts TIMESTAMPTZ := now();
BEGIN
    IF TG_OP = 'INSERT' THEN
        act := 'create'; a := to_jsonb(NEW); uid := NEW.user_id; rid := NEW.id;
    ELSIF TG_OP = 'UPDATE' THEN
        -- nur echte Änderungen an Zeit/Zuordnung/Text protokollieren (nicht den Abgleich mit Plane/Tempo)
        IF (OLD.start_time, OLD.end_time, OLD.duration_seconds, OLD.project_id, OLD.task_id, OLD.comment, OLD.date, OLD.billable, OLD.activity, OLD.is_absence)
           IS NOT DISTINCT FROM
           (NEW.start_time, NEW.end_time, NEW.duration_seconds, NEW.project_id, NEW.task_id, NEW.comment, NEW.date, NEW.billable, NEW.activity, NEW.is_absence) THEN
            RETURN NEW;
        END IF;
        act := 'update'; b := to_jsonb(OLD); a := to_jsonb(NEW); uid := NEW.user_id; rid := NEW.id;
    ELSE
        act := 'delete'; b := to_jsonb(OLD); uid := OLD.user_id; rid := OLD.id;
    END IF;
    PERFORM pg_advisory_xact_lock(727001);   -- Kette: immer nur ein Schreiber
    SELECT hash INTO prev FROM entry_audit ORDER BY id DESC LIMIT 1;
    h := encode(sha256(convert_to(coalesce(prev,'') || '|' || to_char(ts AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') || '|' || uid || '|' || rid || '|' || act || '|' || coalesce(b::text,'') || '|' || coalesce(a::text,''), 'UTF8')), 'hex');
    INSERT INTO entry_audit(ts,user_id,row_id,action,before,after,actor,origin,prev_hash,hash)
        VALUES (ts, uid, rid, act, b, a, NULL, 'db', prev, h);
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER time_entries_audit_trg AFTER INSERT OR UPDATE OR DELETE ON time_entries
    FOR EACH ROW EXECUTE FUNCTION time_entries_audit();

-- audit_log: nur anhängen. Einzige Ausnahme: Löschen einer Person setzt user_id auf NULL (Fremdschlüssel).
CREATE FUNCTION audit_log_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.user_id IS NULL AND OLD.user_id IS NOT NULL
       AND (NEW.id, NEW.at, NEW.action, NEW.detail) IS NOT DISTINCT FROM (OLD.id, OLD.at, OLD.action, OLD.detail) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'Protokoll: Ändern und Löschen nicht erlaubt';
END $$;

CREATE TRIGGER audit_log_guard_trg BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_guard();
