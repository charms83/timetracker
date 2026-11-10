-- Plane-Import (Zeiten der Person AUS Plane, nur auf Anforderung).
-- time_precise: Plane kennt nur die Dauer; importierte Einträge haben Platzhalter-Uhrzeiten (ab 08:00 gestapelt) und sind
--   ausdrücklich „ungenau“ – eigene Spalte statt Ableitung aus source, damit Oberfläche, Prüfungen und spätere Importe
--   (z. B. Tempo ohne Uhrzeit) dasselbe Merkmal nutzen.
-- plane_import_id: Id des Plane-Worklogs, aus dem der Eintrag stammt. Bewusst NICHT plane_worklog_id: das steht für
--   eigene Übertragungen (Altbestand je Eintrag) – die Plane-Übertragung würde einen dort eingetragenen Worklog löschen.
ALTER TABLE time_entries ADD COLUMN time_precise BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE time_entries ADD COLUMN plane_import_id TEXT;
CREATE UNIQUE INDEX time_entries_plane_import_idx ON time_entries(user_id, plane_import_id) WHERE plane_import_id IS NOT NULL;

-- Gelöschte importierte Einträge: nicht beim nächsten Import zurückholen
CREATE TABLE plane_import_ignored (
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plane_import_id TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, plane_import_id)
);

-- Datenbank-Sperre (013): Herkunft und Genauigkeit gehören zu den gesperrten Feldern
CREATE OR REPLACE FUNCTION time_entries_lock() RETURNS trigger LANGUAGE plpgsql AS $$
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
            OLD.activity, OLD.is_absence, OLD.tempo_account, OLD.tempo_skip, OLD.tempo_target, OLD.plane_include, OLD.plane_skip, OLD.time_precise, OLD.plane_import_id)
           IS NOT DISTINCT FROM
           (NEW.user_id, NEW.legacy_id, NEW.start_time, NEW.end_time, NEW.duration_seconds, NEW.project_id, NEW.task_id, NEW.plan_task_id, NEW.comment, NEW.date, NEW.billable, NEW.source,
            NEW.activity, NEW.is_absence, NEW.tempo_account, NEW.tempo_skip, NEW.tempo_target, NEW.plane_include, NEW.plane_skip, NEW.time_precise, NEW.plane_import_id) THEN
            RETURN NEW;
        END IF;
        IF zp_month_locked(OLD.user_id, OLD.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(OLD.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    END IF;
    IF zp_month_locked(NEW.user_id, NEW.date) THEN RAISE EXCEPTION 'Monat % ist eingereicht oder freigegeben', substr(NEW.date,1,7) USING ERRCODE = 'ZP001'; END IF;
    RETURN NEW;
END $$;
