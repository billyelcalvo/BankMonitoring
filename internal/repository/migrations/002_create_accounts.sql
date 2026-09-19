BEGIN;

-- Account ownership must be provisioned by trusted server-side code.
CREATE TABLE accounts (
    id TEXT PRIMARY KEY CHECK (btrim(id) <> ''),
    user_id TEXT NOT NULL CHECK (btrim(user_id) <> '')
);

CREATE INDEX accounts_user_id_idx ON accounts (user_id);

COMMIT;
