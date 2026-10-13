BEGIN;

CREATE TABLE transfers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL CHECK (btrim(user_id) <> ''),
    idempotency_key UUID NOT NULL,
    from_account_id TEXT NOT NULL CHECK (btrim(from_account_id) <> ''),
    to_account_id TEXT NOT NULL CHECK (btrim(to_account_id) <> ''),
    amount BIGINT NOT NULL CHECK (amount > 0),
    currency TEXT NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'cancelled')),
    original_request JSONB NOT NULL CHECK (jsonb_typeof(original_request) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT transfers_different_accounts CHECK (from_account_id <> to_account_id),
    CONSTRAINT transfers_idempotency UNIQUE (user_id, idempotency_key)
);

COMMIT;
