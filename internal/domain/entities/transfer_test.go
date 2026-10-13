package entities

import (
	"errors"
	"strings"
	"testing"
)

func TestCreateTransferValidate(t *testing.T) {
	valid := CreateTransfer{FromAccountID: "a", ToAccountID: "b", Amount: 1, Currency: "PEN"}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*CreateTransfer){
		"missing source":   func(r *CreateTransfer) { r.FromAccountID = " " },
		"missing target":   func(r *CreateTransfer) { r.ToAccountID = "" },
		"same account":     func(r *CreateTransfer) { r.ToAccountID = r.FromAccountID },
		"zero amount":      func(r *CreateTransfer) { r.Amount = 0 },
		"negative amount":  func(r *CreateTransfer) { r.Amount = -5 },
		"lowercase":        func(r *CreateTransfer) { r.Currency = "pen" },
		"short currency":   func(r *CreateTransfer) { r.Currency = "PE" },
		"long description": func(r *CreateTransfer) { r.Description = strings.Repeat("x", 256) },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if err := request.Validate(); !errors.Is(err, ErrInvalidTransfer) {
				t.Fatalf("expected invalid request, got %v", err)
			}
		})
	}
}
