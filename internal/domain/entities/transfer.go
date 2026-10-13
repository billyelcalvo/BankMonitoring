package entities

import (
	"errors"
	"strings"
	"time"

	"bankmonitoring/internal/domain/valueobjects"
)

var ErrInvalidTransfer = errors.New("invalid transfer request")

// CreateTransfer contains the fields a client supplies to request a transfer.
type CreateTransfer struct {
	FromAccountID string `json:"from_account_id"`
	ToAccountID   string `json:"to_account_id"`
	// Amount is expressed in the currency's smallest unit (for example, cents).
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description,omitempty"`
}

// Validate checks the request shape. Rules that need the database (ownership,
// balance, currency of each account) live in the transfer service.
func (r CreateTransfer) Validate() error {
	switch {
	case strings.TrimSpace(r.FromAccountID) == "", strings.TrimSpace(r.ToAccountID) == "":
		return errors.Join(ErrInvalidTransfer, errors.New("both accounts are required"))
	case r.FromAccountID == r.ToAccountID:
		return errors.Join(ErrInvalidTransfer, errors.New("source and destination must differ"))
	case r.Amount <= 0:
		return errors.Join(ErrInvalidTransfer, ErrInvalidAmount)
	case len(r.Currency) != 3 || strings.Trim(r.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "":
		return errors.Join(ErrInvalidTransfer, errors.New("currency must be a three-letter ISO code"))
	case len(r.Description) > 255:
		return errors.Join(ErrInvalidTransfer, errors.New("description is limited to 255 characters"))
	}
	return nil
}

// Transfer represents a transfer with its server-managed state and metadata.
type Transfer struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
	FromAccountID  string `json:"from_account_id"`
	ToAccountID    string `json:"to_account_id"`
	// Amount is expressed in the currency's smallest unit (for example, cents).
	Amount        int64                       `json:"amount"`
	Currency      string                      `json:"currency"`
	Description   string                      `json:"description,omitempty"`
	Status        valueobjects.TransferStatus `json:"status"`
	FailureReason valueobjects.FailureReason  `json:"failure_reason,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}
