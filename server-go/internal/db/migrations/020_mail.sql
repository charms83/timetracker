-- E-Mail-Erinnerungen (notifyApproval): Entprellung je Ereignis/Person/Monat.
-- Inhalt der Mails enthält bewusst keine Zeiten (ISO/Datenschutz).
CREATE TABLE IF NOT EXISTS notify_sent (
    event      TEXT NOT NULL,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    month      TEXT NOT NULL,
    recipient  TEXT NOT NULL DEFAULT '',
    sent_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (event, user_id, month, recipient)
);
CREATE INDEX IF NOT EXISTS notify_sent_at_idx ON notify_sent(sent_at);
