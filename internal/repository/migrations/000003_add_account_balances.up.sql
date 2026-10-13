BEGIN;

-- Balances are stored in the currency's smallest unit, same as transfer amounts.
-- The CHECK is a last line of defence: the service already refuses overdrafts,
-- but if a bug ever slips through, PostgreSQL will abort the transaction.
ALTER TABLE accounts
    ADD COLUMN currency TEXT NOT NULL DEFAULT 'PEN' CHECK (currency ~ '^[A-Z]{3}$'),
    ADD COLUMN balance BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD CONSTRAINT accounts_balance_non_negative CHECK (balance >= 0);

ALTER TABLE accounts ALTER COLUMN currency DROP DEFAULT;

ALTER TABLE transfers
    ADD COLUMN failure_reason TEXT,
    ADD CONSTRAINT transfers_from_account_fk FOREIGN KEY (from_account_id) REFERENCES accounts (id),
    ADD CONSTRAINT transfers_to_account_fk FOREIGN KEY (to_account_id) REFERENCES accounts (id);

CREATE INDEX transfers_from_account_idx ON transfers (from_account_id, created_at DESC);
CREATE INDEX transfers_to_account_idx ON transfers (to_account_id, created_at DESC);

COMMIT;
