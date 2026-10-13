package entities

import (
	"errors"
	"math"
	"testing"
)

func TestDebit(t *testing.T) {
	account := Account{Balance: 100}
	if err := account.Debit(60); err != nil || account.Balance != 40 {
		t.Fatalf("balance=%d err=%v", account.Balance, err)
	}
	if err := account.Debit(41); !errors.Is(err, ErrInsufficientFunds) || account.Balance != 40 {
		t.Fatalf("overdraft must be rejected without touching the balance, got %d %v", account.Balance, err)
	}
	if err := account.Debit(40); err != nil || account.Balance != 0 {
		t.Fatal("should be able to spend the whole balance")
	}
	if err := account.Debit(0); !errors.Is(err, ErrInvalidAmount) {
		t.Fatal("zero debit accepted")
	}
}

func TestCredit(t *testing.T) {
	account := Account{Balance: 10}
	if err := account.Credit(5); err != nil || account.Balance != 15 {
		t.Fatalf("balance=%d err=%v", account.Balance, err)
	}
	if err := account.Credit(-1); !errors.Is(err, ErrInvalidAmount) {
		t.Fatal("negative credit accepted")
	}
	account.Balance = math.MaxInt64 - 1
	if err := account.Credit(2); !errors.Is(err, ErrBalanceOverflow) || account.Balance != math.MaxInt64-1 {
		t.Fatal("overflow not detected")
	}
}
