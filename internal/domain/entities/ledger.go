package entities

// LedgerEntry is one side of a completed transfer: a negative amount for the
// debit and a positive one for the credit. Both sides always add up to zero.
type LedgerEntry struct {
	TransferID   string
	AccountID    string
	Amount       int64
	BalanceAfter int64
}
