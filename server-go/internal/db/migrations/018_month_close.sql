-- Monatsschluss mit Nachfrist (Status 'closed'), Nachtragen durch Teamleitung/Personal (kontrollierte Umgehung der Sperre),
-- Teams aus dem Verzeichnis (Quelle je Team und Mitgliedschaft). Bestehende Daten bleiben unverändert gültig.

-- 1) Neuer Status 'closed' (automatisch geschlossen nach Ablauf der Nachfrist). Name der alten CHECK-Bedingung nicht raten.
DO $$
DECLARE c RECORD; BEGIN
    FOR c IN SELECT conname FROM pg_constraint
             WHERE conrelid = 'month_approvals'::regclass AND contype = 'c' AND pg_get_constraintdef(oid) LIKE '%submitted%' LOOP
        EXECUTE format('ALTER TABLE month_approvals DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;
ALTER TABLE month_approvals ADD CONSTRAINT month_approvals_status_check CHECK (status IN ('submitted','approved','rejected','closed'));

-- 2) Wer hat nachgetragen (NULL = die Person selbst). Nur Anzeige; das Protokoll steht in entry_audit/audit_log.
-- ON DELETE SET NULL ändert nur diese Spalte: time_entries_lock lässt solche Änderungen durch, time_entries_audit protokolliert sie nicht.
ALTER TABLE time_entries ADD COLUMN entered_by BIGINT REFERENCES users(id) ON DELETE SET NULL;

-- 3) Gesperrt sind eingereichte, freigegebene und geschlossene Monate (gemeinsame Sperre je Person wie in 013).
CREATE OR REPLACE FUNCTION zp_month_locked(uid BIGINT, d TEXT) RETURNS BOOLEAN LANGUAGE plpgsql VOLATILE AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(7270030000000000 + uid);
    RETURN EXISTS (SELECT 1 FROM month_approvals WHERE user_id = uid AND month = substr(d, 1, 7) AND status IN ('submitted','approved','closed'));
END $$;

-- Kontrollierte Umgehung: nur in einer Transaktion, in der die Anwendung nach bestandener Berechtigungsprüfung
-- set_config('zp.bypass_user', <Ziel>, true) und set_config('zp.actor', <Akteur>, true) gesetzt hat (Nachtrag, nachtrag.go).
-- Gilt nur für Zeilen genau dieser Person, nie für den Akteur selbst; transaktionslokal, endet mit COMMIT/ROLLBACK.
CREATE FUNCTION zp_bypass(uid BIGINT) RETURNS BOOLEAN LANGUAGE plpgsql VOLATILE AS $$
DECLARE b TEXT := COALESCE(current_setting('zp.bypass_user', true), ''); act TEXT := COALESCE(current_setting('zp.actor', true), ''); BEGIN
    RETURN uid IS NOT NULL AND b <> '' AND b = uid::text AND act ~ '^[1-9][0-9]{0,18}$' AND act <> b;
END $$;

CREATE OR REPLACE FUNCTION time_entries_lock() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE uid BIGINT; BEGIN
    IF TG_OP = 'DELETE' THEN
        uid := OLD.user_id;
        IF NOT EXISTS (SELECT 1 FROM users WHERE id = uid) THEN RETURN OLD; END IF;
        IF zp_month_locked(uid, OLD.date) AND NOT zp_bypass(uid) THEN RAISE EXCEPTION 'Monat % ist geschlossen, eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001'; END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' THEN
        -- Frei bleiben nur Abgleichsfelder (Plane/Tempo-Ergebnis, Version) und entered_by; alles andere ist gesperrt
        IF (OLD.user_id, OLD.legacy_id, OLD.start_time, OLD.end_time, OLD.duration_seconds, OLD.project_id, OLD.task_id, OLD.plan_task_id, OLD.comment, OLD.date, OLD.billable, OLD.source,
            OLD.activity, OLD.is_absence, OLD.tempo_account, OLD.tempo_skip, OLD.tempo_target, OLD.plane_include, OLD.plane_skip)
           IS NOT DISTINCT FROM
           (NEW.user_id, NEW.legacy_id, NEW.start_time, NEW.end_time, NEW.duration_seconds, NEW.project_id, NEW.task_id, NEW.plan_task_id, NEW.comment, NEW.date, NEW.billable, NEW.source,
            NEW.activity, NEW.is_absence, NEW.tempo_account, NEW.tempo_skip, NEW.tempo_target, NEW.plane_include, NEW.plane_skip) THEN
            RETURN NEW;
        END IF;
        IF zp_month_locked(OLD.user_id, OLD.date) AND NOT (zp_bypass(OLD.user_id) AND OLD.user_id = NEW.user_id) THEN
            RAISE EXCEPTION 'Monat % ist geschlossen, eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001';
        END IF;
    END IF;
    IF zp_month_locked(NEW.user_id, NEW.date) AND NOT zp_bypass(NEW.user_id) THEN RAISE EXCEPTION 'Monat % ist geschlossen, eingereicht oder freigegeben', substr(NEW.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION absences_lock() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE r RECORD; m DATE; last DATE; same BOOLEAN := true; BEGIN
    IF TG_OP = 'UPDATE' THEN same := OLD.user_id = NEW.user_id; END IF;
    FOR r IN SELECT * FROM (SELECT CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.user_id END AS uid, CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.date_from END AS f, CASE WHEN TG_OP='INSERT' THEN NULL ELSE OLD.date_to END AS t
             UNION ALL SELECT CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.user_id END, CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.date_from END, CASE WHEN TG_OP='DELETE' THEN NULL ELSE NEW.date_to END) x
             WHERE x.uid IS NOT NULL LOOP
        IF NOT EXISTS (SELECT 1 FROM users WHERE id = r.uid) THEN CONTINUE; END IF;
        m := date_trunc('month', r.f::date)::date; last := r.t::date;
        IF last - m > 800 THEN RAISE EXCEPTION 'Zeitraum zu lang' USING ERRCODE = 'ZP002'; END IF;
        WHILE m <= last LOOP
            IF zp_month_locked(r.uid, to_char(m, 'YYYY-MM-DD')) AND NOT (zp_bypass(r.uid) AND same) THEN
                RAISE EXCEPTION 'Monat % ist geschlossen, eingereicht oder freigegeben', to_char(m,'YYYY-MM') USING ERRCODE = 'ZP001';
            END IF;
            m := (m + interval '1 month')::date;
        END LOOP;
    END LOOP;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;

CREATE OR REPLACE FUNCTION balance_corrections_lock() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF EXISTS (SELECT 1 FROM users WHERE id = OLD.user_id) AND zp_month_locked(OLD.user_id, OLD.date) AND NOT zp_bypass(OLD.user_id) THEN
            RAISE EXCEPTION 'Monat % ist geschlossen, eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001';
        END IF;
        RETURN OLD;
    END IF;
    IF TG_OP = 'UPDATE' AND zp_month_locked(OLD.user_id, OLD.date) AND NOT (zp_bypass(OLD.user_id) AND OLD.user_id = NEW.user_id) THEN
        RAISE EXCEPTION 'Monat % ist geschlossen, eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001';
    END IF;
    IF zp_month_locked(NEW.user_id, NEW.date) AND NOT zp_bypass(NEW.user_id) THEN RAISE EXCEPTION 'Monat % ist geschlossen, eingereicht oder freigegeben', substr(NEW.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    RETURN NEW;
END $$;

-- 4) Änderungsprotokoll: wie 008 (gleiche Hash-Berechnung, gleiche Kette); neu nur actor/origin beim Nachtrag.
-- actor/origin gehen nicht in den Hash ein (wie bisher), die Kette bleibt prüfbar wie zuvor.
CREATE OR REPLACE FUNCTION time_entries_audit() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    prev TEXT; b JSONB; a JSONB; act TEXT; uid BIGINT; rid BIGINT; h TEXT; ts TIMESTAMPTZ := now();
    who TEXT := NULLIF(current_setting('zp.actor', true), ''); orig TEXT := 'db';
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
    IF who IS NOT NULL THEN
        orig := 'nachtrag';
    END IF;
    PERFORM pg_advisory_xact_lock(727001);   -- Kette: immer nur ein Schreiber
    SELECT hash INTO prev FROM entry_audit ORDER BY id DESC LIMIT 1;
    h := encode(sha256(convert_to(coalesce(prev,'') || '|' || to_char(ts AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS.US"Z"') || '|' || uid || '|' || rid || '|' || act || '|' || coalesce(b::text,'') || '|' || coalesce(a::text,''), 'UTF8')), 'hex');
    INSERT INTO entry_audit(ts,user_id,row_id,action,before,after,actor,origin,prev_hash,hash)
        VALUES (ts, uid, rid, act, b, a, who, orig, prev, h);
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END $$;

-- 5) Teams: Quelle 'local' (Admin-Oberfläche/Befehl) oder 'directory' (Gruppen <präfix><name> / <präfix><name>-lead).
ALTER TABLE teams ADD COLUMN source TEXT NOT NULL DEFAULT 'local' CHECK (source IN ('local','directory'));
ALTER TABLE team_members ADD COLUMN source TEXT NOT NULL DEFAULT 'local' CHECK (source IN ('local','directory'));

-- Suche der offenen Nachtrag-Hinweise je Person
CREATE INDEX month_approval_log_action_idx ON month_approval_log(user_id, action, at);
