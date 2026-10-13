package repository

import "context"

// Tx exposes repositories bound to a single database transaction.
type Tx interface {
	Accounts() AccountRepository
	Transfers() TransferRepository
	Ledger() LedgerRepository
}

// TxManager runs fn in a transaction: it commits when fn returns nil and
// rolls back otherwise, so business rules can bail out with a plain error.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(Tx) error) error
}
