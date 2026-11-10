-- Rollen als kombinierbare Merkmale, Teams und Monatsfreigabe.
-- Mitarbeiter sieht sich selbst; Teamleiter (team_members.is_lead) sein Team; Personal (users.is_hr) alle.
ALTER TABLE users ADD COLUMN is_hr BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE teams (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX teams_name_idx ON teams (lower(name));

CREATE TABLE team_members (
    team_id  BIGINT NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    user_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_lead  BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX team_members_user_idx ON team_members(user_id);

-- Freigabe je Person und Monat: eingereicht → freigegeben | abgelehnt. Eingereichte und freigegebene Monate sind schreibgeschützt.
CREATE TABLE month_approvals (
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    month        TEXT NOT NULL CHECK (month ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
    status       TEXT NOT NULL CHECK (status IN ('submitted','approved','rejected')),
    submitted_at TIMESTAMPTZ,
    decided_at   TIMESTAMPTZ,
    decided_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    comment      TEXT,
    PRIMARY KEY (user_id, month)
);

-- Verlauf: wer hat wann was getan (nur anhängen)
CREATE TABLE month_approval_log (
    id       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id  BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    month    TEXT NOT NULL,
    action   TEXT NOT NULL,
    actor_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    comment  TEXT
);
CREATE INDEX month_approval_log_idx ON month_approval_log(user_id, month);

-- Sperre in der Datenbank selbst (greift für alle Wege: Eingabe, Timer, Raster, Abgleich).
-- Mit Person-Löschung (Kaskade) und Änderungen, die nur Abgleichsfelder betreffen, gibt es keine Sperre.
-- Gemeinsame Sperre je Person: Einreichen/Freigeben nehmen sie exklusiv (gleicher Schlüssel in approval.go), Schreibzugriffe gemeinsam.
-- Dadurch kann keine bereits laufende Änderung noch in einen Monat gelangen, der gerade eingereicht oder freigegeben wird.
CREATE FUNCTION zp_month_locked(uid BIGINT, d TEXT) RETURNS BOOLEAN LANGUAGE plpgsql VOLATILE AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(7270030000000000 + uid);
    RETURN EXISTS (SELECT 1 FROM month_approvals WHERE user_id = uid AND month = substr(d, 1, 7) AND status IN ('submitted','approved'));
END $$;

CREATE FUNCTION time_entries_lock() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE uid BIGINT; BEGIN
    IF TG_OP = 'DELETE' THEN
        uid := OLD.user_id;
        IF NOT EXISTS (SELECT 1 FROM users WHERE id = uid) THEN RETURN OLD; END IF;
        IF zp_month_locked(uid, OLD.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001'; END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        -- Frei bleiben nur Abgleichsfelder (Plane/Tempo-Ergebnis, Version); alles andere – auch Ausschluss/Einschluss, Konto, Quelle – ist gesperrt
        IF (OLD.user_id, OLD.legacy_id, OLD.start_time, OLD.end_time, OLD.duration_seconds, OLD.project_id, OLD.task_id, OLD.plan_task_id, OLD.comment, OLD.date, OLD.billable, OLD.source,
            OLD.activity, OLD.is_absence, OLD.tempo_account, OLD.tempo_skip, OLD.tempo_target, OLD.plane_include, OLD.plane_skip)
           IS NOT DISTINCT FROM
           (NEW.user_id, NEW.legacy_id, NEW.start_time, NEW.end_time, NEW.duration_seconds, NEW.project_id, NEW.task_id, NEW.plan_task_id, NEW.comment, NEW.date, NEW.billable, NEW.source,
            NEW.activity, NEW.is_absence, NEW.tempo_account, NEW.tempo_skip, NEW.tempo_target, NEW.plane_include, NEW.plane_skip) THEN
            RETURN NEW;
        END IF;
        IF zp_month_locked(OLD.user_id, OLD.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    END IF;
    IF zp_month_locked(NEW.user_id, NEW.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(NEW.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER time_entries_lock_trg BEFORE INSERT OR UPDATE OR DELETE ON time_entries FOR EACH ROW EXECUTE FUNCTION time_entries_lock();

-- Abwesenheiten: Beginn- und Endmonat prüfen (mehrmonatige Zeiträume: jeder berührte Monat zählt)
CREATE FUNCTION absences_lock() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r RECORD; m DATE; last DATE; BEGIN
    FOR r IN SELECT * FROM (SELECT CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.user_id END AS uid, CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.date_from END AS f, CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.date_to END AS t
             UNION ALL SELECT CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.user_id END, CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.date_from END, CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.date_to END) x
             WHERE x.uid IS NOT NULL LOOP
        IF NOT EXISTS (SELECT 1 FROM users WHERE id = r.uid) THEN CONTINUE; END IF;
        m := date_trunc('month', r.f::date)::date; last := r.t::date;
        IF last - m > 800 THEN RAISE EXCEPTION 'Zeitraum zu lang' USING ERRCODE = 'ZP002'; END IF;
        WHILE m <= last LOOP
            IF zp_month_locked(r.uid, to_char(m, 'YYYY-MM-DD')) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', to_char(m,'YYYY-MM') USING ERRCODE = 'ZP001'; END IF;
            m := (m + interval '1 month')::date;
        END LOOP;
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER absences_lock_trg BEFORE INSERT OR UPDATE OR DELETE ON absences FOR EACH ROW EXECUTE FUNCTION absences_lock();

CREATE FUNCTION balance_corrections_lock() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) AND zp_month_locked(OLD.user_id, OLD.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001'; END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' AND zp_month_locked(OLD.user_id, OLD.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    IF zp_month_locked(NEW.user_id, NEW.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(NEW.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER balance_corrections_lock_trg BEFORE INSERT OR UPDATE OR DELETE ON balance_corrections FOR EACH ROW EXECUTE FUNCTION balance_corrections_lock();
