-- Laufende OIDC-Anmeldungen (einmalig, kurzlebig): id = Hash des Browser-Cookies
CREATE TABLE oidc_flows (
    id         TEXT PRIMARY KEY,
    state      TEXT NOT NULL,
    nonce      TEXT NOT NULL,
    verifier   TEXT NOT NULL,
    next_path  TEXT NOT NULL DEFAULT '/admin',
    expires_at TIMESTAMPTZ NOT NULL
);
