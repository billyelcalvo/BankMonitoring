BEGIN;

CREATE TABLE users (
    id TEXT PRIMARY KEY CHECK (btrim(id) <> ''),
    email TEXT NOT NULL CHECK (btrim(email) <> ''),
    password_hash TEXT NOT NULL,
    permissions TEXT[] NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_unique_idx ON users (lower(email));

ALTER TABLE accounts
    ADD CONSTRAINT accounts_user_fk FOREIGN KEY (user_id) REFERENCES users (id);

COMMIT;
