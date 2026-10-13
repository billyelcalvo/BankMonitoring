package repository

import (
	"context"
	"fmt"

	"bankmonitoring/internal/domain/entities"
	domainrepository "bankmonitoring/internal/domain/repository"
)

type LedgerRepository struct {
	db querier
}

var _ domainrepository.LedgerRepository = (*LedgerRepository)(nil)

func (r *LedgerRepository) Append(ctx context.Context, entries ...entities.LedgerEntry) error {
	transferIDs := make([]string, len(entries))
	accountIDs := make([]string, len(entries))
	amounts := make([]int64, len(entries))
	balances := make([]int64, len(entries))
	for i, e := range entries {
		transferIDs[i], accountIDs[i], amounts[i], balances[i] = e.TransferID, e.AccountID, e.Amount, e.BalanceAfter
	}
	// One round trip for both sides of the transfer.
	_, err := r.db.Exec(ctx, `INSERT INTO ledger_entries (transfer_id, account_id, amount, balance_after)
		SELECT * FROM unnest($1::uuid[], $2::text[], $3::bigint[], $4::bigint[])`,
		transferIDs, accountIDs, amounts, balances)
	if err != nil {
		return fmt.Errorf("append ledger entries: %w", err)
	}
	return nil
}
