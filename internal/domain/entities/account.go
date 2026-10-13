package entities

import (
	"errors"
	"math"
	"time"
)

var (
	ErrInvalidAmount     = errors.New("amount must be positive")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrBalanceOverflow   = errors.New("balance would overflow")
)

// Account holds money in a single currency. Balance uses the currency's
// smallest unit, so there's no floating point anywhere near the money.
type Account struct {
	ID        string    `json:"id"`
	UserID    string    `json:"-"`
	Currency  string    `json:"currency"`
	Balance   int64     `json:"balance"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Debit only changes the in-memory value; persisting it is the caller's job,
// and it must happen inside the transaction that locked the row.
func (a *Account) Debit(amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if a.Balance < amount {
		return ErrInsufficientFunds
	}
	a.Balance -= amount
	return nil
}

func (a *Account) Credit(amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if a.Balance > math.MaxInt64-amount {
		return ErrBalanceOverflow
	}
	a.Balance += amount
	return nil
}
