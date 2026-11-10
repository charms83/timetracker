-- API-Token als Geräte-Art (REST-Zugang per Bearer zpdev_…); gleiche Tabelle device_tokens.
ALTER TABLE device_tokens DROP CONSTRAINT IF EXISTS device_tokens_kind_check;
ALTER TABLE device_tokens ADD CONSTRAINT device_tokens_kind_check
  CHECK (kind IN ('mac', 'addon', 'minimal', 'other', 'api'));
