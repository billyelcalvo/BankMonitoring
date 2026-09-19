package transfer

import "time"

// TransferStatus represents the current state of a transfer.
type TransferStatus string

const (
	TransferStatusPending    TransferStatus = "pending"
	TransferStatusProcessing TransferStatus = "processing"
	TransferStatusCompleted  TransferStatus = "completed"
	TransferStatusFailed     TransferStatus = "failed"
	TransferStatusCancelled  TransferStatus = "cancelled"
)

// CreateTransfer contains the fields a client supplies to request a transfer.
type CreateTransfer struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	// Amount is expressed in the currency's smallest unit (for example, cents).
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description,omitempty"`
}

// Transfer represents a transfer with its server-managed state and metadata.
type Transfer struct {
	ID            string `json:"id"`
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	// Amount is expressed in the currency's smallest unit (for example, cents).
	Amount      int64          `json:"amount"`
	Currency    string         `json:"currency"`
	Description string         `json:"description,omitempty"`
	Status      TransferStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}
