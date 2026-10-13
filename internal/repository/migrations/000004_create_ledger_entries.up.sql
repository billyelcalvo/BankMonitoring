BEGIN;

-- Double-entry ledger: every completed transfer writes one debit and one credit.
-- The sum of amounts per transfer is always zero, which makes reconciliation
-- (balance = opening balance + sum of entries) a simple query.
CREATE TABLE ledger_entries (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    transfer_id UUID NOT NULL REFERENCES transfers (id),
    account_id TEXT NOT NULL REFERENCES accounts (id),
    amount BIGINT NOT NULL CHECK (amount <> 0),
    balance_after BIGINT NOT NULL CHECK (balance_after >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ledger_entries_account_idx ON ledger_entries (account_id, id);
CREATE INDEX ledger_entries_transfer_idx ON ledger_entries (transfer_id);

COMMIT;
