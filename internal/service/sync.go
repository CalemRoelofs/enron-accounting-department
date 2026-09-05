package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/calemroelofs/enron-accounting-department/internal/config"
)

const centsPerEuro = 100

// EnableBankingAmount represents an amount in the Enable Banking API.
type EnableBankingAmount struct {
	Value    string `json:"value"`
	Currency string `json:"currency"`
}

// EnableBankingAccount represents an account in the Enable Banking API.
type EnableBankingAccount struct {
	IBAN string `json:"iban"`
}

// EnableBankingTransaction represents a transaction in the Enable Banking API.
type EnableBankingTransaction struct {
	TransactionID                     string               `json:"transaction_id"`
	BookingDate                       string               `json:"booking_date"`
	ValueDate                         string               `json:"value_date"`
	Amount                            EnableBankingAmount  `json:"amount"`
	CreditorName                      string               `json:"creditor_name"`
	CreditorAccount                   EnableBankingAccount `json:"creditor_account"`
	DebtorAccount                     EnableBankingAccount `json:"debtor_account"`
	RemittanceInformationUnstructured string               `json:"remittance_information_unstructured"`
	Status                            string               `json:"status"`
}

// EnableBankingResponse represents a response from the Enable Banking API.
type EnableBankingResponse struct {
	Transactions    []EnableBankingTransaction `json:"transactions"`
	ContinuationKey *string                    `json:"continuation_key"`
}

// SyncResult holds the result of a sync operation.
type SyncResult struct {
	Status           string `json:"status"`
	Ingested         int    `json:"ingested"`
	TransfersMatched int    `json:"transfers_matched"`
	PayPeriodRolled  bool   `json:"pay_period_rolled"`
}

// SyncError holds sync error details.
type SyncError struct {
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	ReauthURL string `json:"reauth_url,omitempty"`
	Message   string `json:"message,omitempty"`
}

// ParseAmountCents parses a monetary value string to cents.
func ParseAmountCents(value string) (int64, error) {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing amount %q: %w", value, err)
	}
	return int64(math.Round(f * centsPerEuro)), nil
}

// SyncFromFixture ingests transactions from an Enable Banking fixture.
//
//nolint:gocognit,govet,noctx,funlen // complexity, err shadowing, context, length by design
func (s *Service) SyncFromFixture(cfg *config.Config, fixtureData []byte) (*SyncResult, error) {
	var resp EnableBankingResponse
	if err := json.Unmarshal(fixtureData, &resp); err != nil {
		return nil, fmt.Errorf("parsing fixture: %w", err)
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result := &SyncResult{Status: statusSuccess}

	for _, t := range resp.Transactions {
		amountCents, err := ParseAmountCents(t.Amount.Value)
		if err != nil {
			return nil, fmt.Errorf("transaction %s: %w", t.TransactionID, err)
		}

		bookDate, err := time.Parse("2006-01-02", t.BookingDate)
		if err != nil {
			return nil, fmt.Errorf("parsing date %s: %w", t.BookingDate, err)
		}

		creditorIBAN := t.CreditorAccount.IBAN
		debtorIBAN := t.DebtorAccount.IBAN

		isTransfer, err := s.MatchInterAccountTransfer(tx, creditorIBAN, debtorIBAN)
		if err != nil {
			return nil, fmt.Errorf("matching inter-account transfer: %w", err)
		}

		category := ""
		if isTransfer {
			category = "Transfer_Internal"
			result.TransfersMatched++
		} else {
			match := MatchCounterparty(creditorIBAN, debtorIBAN, cfg.KnownCounterparties)
			if match.Matched {
				category = match.Category
			}
		}

		isSalary := DetectSalary(
			parseAmountFloat(t.Amount.Value),
			debtorIBAN,
			t.RemittanceInformationUnstructured,
			cfg.EmployerIBAN,
		)
		if isSalary && !isTransfer && ListaPlacRe.MatchString(t.RemittanceInformationUnstructured) {
			category = "Salary"
		}

		var accountID int64
		err = tx.QueryRow(
			"SELECT id FROM accounts WHERE iban = ? OR iban = ? LIMIT 1",
			creditorIBAN, debtorIBAN,
		).Scan(&accountID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("no account found for IBANs in transaction %s", t.TransactionID)
			}
			return nil, fmt.Errorf("looking up account: %w", err)
		}

		var transferTitle *string
		if t.RemittanceInformationUnstructured != "" {
			transferTitle = &t.RemittanceInformationUnstructured
		}

		tagsJSON := "[]"

		_, err = tx.Exec(
			`INSERT INTO transactions 
			(bank_transaction_id, account_id, date, amount_cents, currency, 
			 merchant_name, transfer_title, creditor_iban, debtor_iban, 
			 category, tags, notes, needs_review) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.TransactionID, accountID, t.BookingDate, amountCents, t.Amount.Currency,
			t.CreditorName, transferTitle, creditorIBAN, debtorIBAN,
			category, tagsJSON, nil, 0,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting transaction %s: %w", t.TransactionID, err)
		}

		result.Ingested++

		if isSalary && category == "Salary" {
			rolled, err := s.RollPayPeriod(tx, bookDate, cfg.SalaryMinGapDays)
			if err != nil {
				return nil, fmt.Errorf("rolling pay period: %w", err)
			}
			if rolled {
				result.PayPeriodRolled = true
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing transaction: %w", err)
	}

	return result, nil
}

func parseAmountFloat(value string) float64 {
	f, _ := strconv.ParseFloat(value, 64)
	return f
}

// CategorizeTransaction sets the category, tags, and notes for a transaction.
//
//nolint:noctx // deliberate: DB.Exec uses background context implicitly
func (s *Service) CategorizeTransaction(id int64, category string, tags []string, notes *string) error {
	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return fmt.Errorf("marshaling tags: %w", err)
	}

	result, err := s.DB.Exec(
		"UPDATE transactions SET category = ?, tags = ?, notes = ? WHERE id = ?",
		category, string(tagsJSON), notes, id,
	)
	if err != nil {
		return fmt.Errorf("updating transaction: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("getting rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("transaction %d not found", id)
	}

	return nil
}
