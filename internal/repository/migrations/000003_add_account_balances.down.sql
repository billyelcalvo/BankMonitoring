BEGIN;

DROP INDEX IF EXISTS transfers_to_account_idx;
DROP INDEX IF EXISTS transfers_from_account_idx;

ALTER TABLE transfers
    DROP CONSTRAINT IF EXISTS transfers_to_account_fk,
    DROP CONSTRAINT IF EXISTS transfers_from_account_fk,
    DROP COLUMN IF EXISTS failure_reason;

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_balance_non_negative,
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS created_at,
    DROP COLUMN IF EXISTS balance,
    DROP COLUMN IF EXISTS currency;

COMMIT;
