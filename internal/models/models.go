// Package models defines data types for the application.
package models

import "time"

// Account represents a bank account.
type Account struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	IBAN           string `json:"iban"`
	ExternalID     string `json:"external_id"`
	Type           string `json:"type"`
	VirtualBalance int64  `json:"virtual_balance"`
}

// Transaction represents a financial transaction.
type Transaction struct {
	ID                int64     `json:"id"`
	BankTransactionID string    `json:"bank_transaction_id"`
	AccountID         int64     `json:"account_id"`
	Date              time.Time `json:"date"`
	AmountCents       int64     `json:"amount_cents"`
	Currency          string    `json:"currency"`
	MerchantName      string    `json:"merchant_name"`
	TransferTitle     *string   `json:"transfer_title,omitempty"`
	CreditorIBAN      string    `json:"creditor_iban"`
	DebtorIBAN        string    `json:"debtor_iban"`
	Category          string    `json:"category"`
	Tags              []string  `json:"tags"`
	Notes             *string   `json:"notes,omitempty"`
	NeedsReview       bool      `json:"needs_review"`
}

// LineItem represents a line item within a transaction.
type LineItem struct {
	ID            int64    `json:"id"`
	TransactionID int64    `json:"transaction_id"`
	ItemName      string   `json:"item_name"`
	AmountCents   int64    `json:"amount_cents"`
	Category      string   `json:"category"`
	Tags          []string `json:"tags"`
	Notes         *string  `json:"notes,omitempty"`
}

// PayPeriod represents a salary pay period.
type PayPeriod struct {
	ID        int64      `json:"id"`
	StartDate time.Time  `json:"start_date"`
	EndDate   *time.Time `json:"end_date,omitempty"`
}

// BankConnection represents a connection to a banking provider.
type BankConnection struct {
	ID               int64     `json:"id"`
	Provider         string    `json:"provider"`
	SessionID        string    `json:"session_id,omitempty"`
	AspspID          string    `json:"aspsp_id,omitempty"`
	ConsentGrantedAt time.Time `json:"consent_granted_at"`
	ConsentExpiresAt time.Time `json:"consent_expires_at"`
}
