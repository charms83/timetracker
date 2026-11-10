-- Mehrere Jira-Server (Instanzen), vom Administrator verwaltet. Jira-Token je Person UND Instanz.
-- Stufe jetzt: nutzbar nur kind 'dc' mit tempo 'dc' oder 'none'; 'cloud' lässt sich anlegen, bleibt aber aus („folgt“).
-- Übernahme: server_config.jira_base_url → Instanz „Jira“ (eingeschaltet); abweichende Adressen aus user_settings
-- → weitere Instanzen, AUS (nie vom Administrator geprüft). Tokens, Tempo-Zuordnungen und Sperren bekommen die Instanz,
-- an die sie bisher tatsächlich gingen (Server-Adresse vor Personen-Adresse).

CREATE TABLE jira_instances (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       TEXT NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 60),
    kind       TEXT NOT NULL DEFAULT 'dc' CHECK (kind IN ('dc','cloud')),
    base_url   TEXT NOT NULL,
    tempo      TEXT NOT NULL DEFAULT 'dc' CHECK (tempo IN ('dc','cloud','none')),
    enabled    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (NOT enabled OR (kind = 'dc' AND tempo IN ('dc','none')))   -- Cloud folgt: nicht einschaltbar
);
CREATE UNIQUE INDEX jira_instances_name_idx ON jira_instances (lower(btrim(name)));

INSERT INTO jira_instances(name, kind, base_url, tempo, enabled)
SELECT 'Jira', 'dc', rtrim(btrim(value), '/'), 'dc', true FROM server_config WHERE key = 'jira_base_url' AND btrim(value) <> '';

INSERT INTO jira_instances(name, kind, base_url, tempo, enabled)
SELECT CASE WHEN x.rn = 1 AND x.c0 = 0 THEN 'Jira' ELSE 'Jira ' || (x.rn + x.c0) END, 'dc', x.u, 'dc', false
FROM (SELECT u, row_number() OVER (ORDER BY u) AS rn, (SELECT COUNT(*) FROM jira_instances) AS c0
      FROM (SELECT DISTINCT rtrim(btrim(value), '/') AS u FROM user_settings WHERE key = 'jira_base_url' AND btrim(value) <> '') d
      WHERE d.u NOT IN (SELECT base_url FROM jira_instances)) x;

-- Instanz, an die die Person bisher gebucht hat (Server-Adresse hat Vorrang, sonst eigene Adresse)
CREATE TEMP TABLE zp_user_inst ON COMMIT DROP AS
SELECT u.id AS user_id, i.id AS instance_id
FROM users u
JOIN jira_instances i ON i.base_url = COALESCE(
    (SELECT rtrim(btrim(value), '/') FROM server_config WHERE key = 'jira_base_url' AND btrim(value) <> ''),
    (SELECT rtrim(btrim(value), '/') FROM user_settings s WHERE s.user_id = u.id AND s.key = 'jira_base_url' AND btrim(value) <> ''));

-- Tokens je Instanz (scope = Instanz-Id; 0 = ohne Instanz, z. B. plane_key). Alte Blobs liest der Selbsttest und versiegelt sie neu.
ALTER TABLE user_secrets ADD COLUMN scope BIGINT NOT NULL DEFAULT 0;
ALTER TABLE user_secrets DROP CONSTRAINT user_secrets_pkey;
ALTER TABLE user_secrets ADD PRIMARY KEY (user_id, kind, scope);
UPDATE user_secrets s SET scope = m.instance_id FROM zp_user_inst m WHERE s.kind = 'jira_token' AND s.user_id = m.user_id;

-- Einstellungen je Person und Instanz: Schlüssel „i<Id>:<Name>“ (nicht über /api/settings änderbar)
INSERT INTO user_settings(user_id, key, value)
SELECT s.user_id, 'i' || m.instance_id || ':' || s.key, s.value
FROM user_settings s JOIN zp_user_inst m ON m.user_id = s.user_id
WHERE s.key IN ('jira_username','jira_userkey','jira_project_keys','tempo_account','tempo_account_attr_id','tempo_booking_issue',
                'jira_new_project','jira_new_issuetype','tempo_push_since')
ON CONFLICT (user_id, key) DO NOTHING;
DELETE FROM user_settings WHERE key = 'jira_base_url';
DELETE FROM server_config WHERE key = 'jira_base_url';

-- Tempo-Zuordnung Eintrag ↔ Instanz (bereits gebuchte Worklogs bleiben dort, kein zweites Anlegen)
ALTER TABLE time_entries ADD COLUMN tempo_push_instance BIGINT REFERENCES jira_instances(id) ON DELETE SET NULL;
UPDATE time_entries e SET tempo_push_instance = m.instance_id FROM zp_user_inst m
WHERE e.user_id = m.user_id AND e.tempo_push_id IS NOT NULL;

ALTER TABLE tempo_push_deletions ADD COLUMN instance_id BIGINT REFERENCES jira_instances(id) ON DELETE SET NULL;
UPDATE tempo_push_deletions d SET instance_id = m.instance_id FROM zp_user_inst m WHERE d.user_id = m.user_id;

-- Sperren und Übergangs-Tickets je Instanz (0 = keine Zuordnung möglich, wird nicht mehr benutzt)
ALTER TABLE tempo_months ADD COLUMN instance_id BIGINT NOT NULL DEFAULT 0;
UPDATE tempo_months t SET instance_id = m.instance_id FROM zp_user_inst m WHERE t.user_id = m.user_id;
ALTER TABLE tempo_months DROP CONSTRAINT tempo_months_pkey;
ALTER TABLE tempo_months ADD PRIMARY KEY (user_id, instance_id, month);

ALTER TABLE tempo_month_tickets ADD COLUMN instance_id BIGINT NOT NULL DEFAULT 0;
UPDATE tempo_month_tickets t SET instance_id = m.instance_id FROM zp_user_inst m WHERE t.user_id = m.user_id;
ALTER TABLE tempo_month_tickets DROP CONSTRAINT tempo_month_tickets_pkey;
ALTER TABLE tempo_month_tickets ADD PRIMARY KEY (user_id, instance_id, month, worker);
