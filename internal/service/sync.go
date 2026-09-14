package service

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/calemroelofs/enron-accounting-department/internal/config"
	"github.com/calemroelofs/enron-accounting-department/internal/enablebanking"
)

const (
	centsPerEuro   = 100
	categorySalary = "Salary"
)

// EnableBankingAmount represents an amount in the Enable Banking API.
//
// Deprecated: use enablebanking.EnableBankingAmount.
type EnableBankingAmount = enablebanking.EnableBankingAmount

// EnableBankingAccount represents an account in the Enable Banking API.
//
// Deprecated: use enablebanking.EnableBankingAccount.
type EnableBankingAccount = enablebanking.EnableBankingAccount

// EnableBankingTransaction represents a transaction in the Enable Banking API.
//
// Deprecated: use enablebanking.Transaction.
type EnableBankingTransaction = enablebanking.Transaction

// EnableBankingResponse represents a response from the Enable Banking API.
type EnableBankingResponse struct {
	Transactions    []EnableBankingTransaction `json:"transactions"`
	ContinuationKey *string                    `json:"continuation_key"`
}

// SyncResult holds the result of a sync operation.
type SyncResult struct {
	Status           string   `json:"status"`
	Ingested         int      `json:"ingested"`
	TransfersMatched int      `json:"transfers_matched"`
	PayPeriodRolled  bool     `json:"pay_period_rolled"`
	SkippedAccounts  []string `json:"skipped_accounts,omitempty"`
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

	rules, ruleErr := s.listRulesTx(tx)
	if ruleErr != nil {
		return nil, fmt.Errorf("loading rules: %w", ruleErr)
	}

	sort.Slice(resp.Transactions, func(i, j int) bool {
		return resp.Transactions[i].BookingDate < resp.Transactions[j].BookingDate
	})

	for _, t := range resp.Transactions {
		amountCents, err := ParseAmountCents(t.TransactionAmount.Value)
		if err != nil {
			return nil, fmt.Errorf("transaction %s: %w", t.TransactionID(), err)
		}

		bookDate, err := time.Parse("2006-01-02", t.BookingDate)
		if err != nil {
			return nil, fmt.Errorf("parsing date %s: %w", t.BookingDate, err)
		}

		creditorIBAN := t.CreditorAccount.IBAN()
		debtorIBAN := t.DebtorAccount.IBAN()

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
			parseAmountFloat(t.TransactionAmount.Value),
			debtorIBAN,
			t.RemittanceInfo(),
			cfg.EmployerIBAN,
		)
		if isSalary && !isTransfer && ListaPlacRe.MatchString(t.RemittanceInfo()) {
			category = categorySalary
		}

		var accountID int64
		err = tx.QueryRow(
			"SELECT id FROM accounts WHERE iban = ? OR iban = ? LIMIT 1",
			creditorIBAN, debtorIBAN,
		).Scan(&accountID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("no account found for IBANs in transaction %s", t.TransactionID())
			}
			return nil, fmt.Errorf("looking up account: %w", err)
		}

		var transferTitle *string
		info := t.RemittanceInfo()
		if info != "" {
			transferTitle = &info
		}

		tagsJSON := "[]"

		if category == "" {
			if matched := MatchRule(rules, t.RemittanceInfo(), creditorIBAN); matched != nil {
				category = matched.Category
				tagsJSON = matched.Tags
			}
		}

		if t.CreditDebitIndicator == "DBIT" {
			amountCents = -amountCents
		}

		res, err := tx.Exec(
			`INSERT OR IGNORE INTO transactions 
			(bank_transaction_id, account_id, date, amount_cents, currency, 
			 merchant_name, transfer_title, creditor_iban, debtor_iban,
			 credit_debit_indicator,
			 category, tags, notes, needs_review) 
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.TransactionID(), accountID, t.BookingDate, amountCents, t.TransactionAmount.Currency,
			t.MerchantName(), transferTitle, creditorIBAN, debtorIBAN,
			t.CreditDebitIndicator,
			category, tagsJSON, nil, 0,
		)
		if err != nil {
			return nil, fmt.Errorf("inserting transaction %s: %w", t.TransactionID(), err)
		}

		inserted, err := res.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("checking insert for transaction %s: %w", t.TransactionID(), err)
		}

		result.Ingested++

		if inserted == 1 && isSalary && category == categorySalary {
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

// TransactionFetcher is an interface for fetching transactions from Enable Banking.
type TransactionFetcher interface {
	GetTransactions(accountID, continuationKey string, dateFrom time.Time) ([]enablebanking.Transaction, *string, error)
}

// SyncFromAPI fetches transactions from Enable Banking and ingests them.
//
//nolint:cyclop,gocyclo,gocognit,funlen,noctx,govet // complexity, context, shadow by design
func (s *Service) SyncFromAPI(
	cfg *config.Config,
	fetcher TransactionFetcher,
	accountID int64,
) ([]enablebanking.Transaction, *SyncResult, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	result := &SyncResult{Status: statusSuccess}

	rules, ruleErr := s.listRulesTx(tx)
	if ruleErr != nil {
		return nil, nil, fmt.Errorf("loading rules: %w", ruleErr)
	}

	rows, err := tx.Query("SELECT id, external_id, iban FROM accounts")
	if err != nil {
		return nil, nil, fmt.Errorf("listing accounts: %w", err)
	}
	defer rows.Close()

	type accountRow struct {
		ID         int64
		ExternalID string
		IBAN       string
	}
	var accounts []accountRow
	for rows.Next() {
		var a accountRow
		if err := rows.Scan(&a.ID, &a.ExternalID, &a.IBAN); err != nil {
			return nil, nil, fmt.Errorf("scanning account: %w", err)
		}
		accounts = append(accounts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("rows iteration error: %w", err)
	}

	if len(accounts) == 0 {
		return nil, nil, fmt.Errorf("no accounts configured for sync")
	}

	if accountID > 0 {
		var filtered []accountRow
		for _, a := range accounts {
			if a.ID == accountID {
				filtered = append(filtered, a)
			}
		}
		if len(filtered) == 0 {
			return nil, nil, fmt.Errorf("account %d not found", accountID)
		}
		accounts = filtered
	}

	var allTxs []enablebanking.Transaction
	for _, acc := range accounts {
		if strings.TrimSpace(acc.ExternalID) == "" {
			identifier := strings.TrimSpace(acc.IBAN)
			if identifier == "" {
				identifier = fmt.Sprintf("id=%d", acc.ID)
			}
			result.SkippedAccounts = append(result.SkippedAccounts, identifier)
			continue
		}

		var accountTxs []enablebanking.Transaction
		var ck string
		for {
			txs, nextKey, err := fetcher.GetTransactions(acc.ExternalID, ck, time.Time{})
			if err != nil {
				return nil, nil, fmt.Errorf("fetching transactions for account %s: %w", acc.ExternalID, err)
			}
			accountTxs = append(accountTxs, txs...)
			if nextKey == nil || *nextKey == "" {
				break
			}
			ck = *nextKey
		}

		sort.Slice(accountTxs, func(i, j int) bool {
			return accountTxs[i].BookingDate < accountTxs[j].BookingDate
		})

		for _, t := range accountTxs {
			amountCents, err := ParseAmountCents(t.TransactionAmount.Value)
			if err != nil {
				return nil, nil, fmt.Errorf("transaction %s: %w", t.TransactionID(), err)
			}

			bookDate, err := time.Parse("2006-01-02", t.BookingDate)
			if err != nil {
				return nil, nil, fmt.Errorf("parsing date %s: %w", t.BookingDate, err)
			}

			creditorIBAN := t.CreditorAccount.IBAN()
			debtorIBAN := t.DebtorAccount.IBAN()

			isTransfer, err := s.MatchInterAccountTransfer(tx, creditorIBAN, debtorIBAN)
			if err != nil {
				return nil, nil, fmt.Errorf("matching inter-account transfer: %w", err)
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
				parseAmountFloat(t.TransactionAmount.Value),
				debtorIBAN,
				t.RemittanceInfo(),
				cfg.EmployerIBAN,
			)
			if isSalary && !isTransfer && ListaPlacRe.MatchString(t.RemittanceInfo()) {
				category = categorySalary
			}

			var accountID int64
			// Try to match by IBAN first, fall back to the account we're currently syncing
			err = tx.QueryRow(
				"SELECT id FROM accounts WHERE iban = ? OR iban = ? LIMIT 1",
				creditorIBAN, debtorIBAN,
			).Scan(&accountID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					accountID = acc.ID
				} else {
					return nil, nil, fmt.Errorf("looking up account for transaction %s: %w", t.TransactionID(), err)
				}
			}

			var transferTitle *string
			info := t.RemittanceInfo()
			if info != "" {
				transferTitle = &info
			}

			tagsJSON := "[]"

			if category == "" {
				if matched := MatchRule(rules, t.RemittanceInfo(), creditorIBAN); matched != nil {
					category = matched.Category
					tagsJSON = matched.Tags
				}
			}

			if t.CreditDebitIndicator == "DBIT" {
				amountCents = -amountCents
			}

			res, err := tx.Exec(
				`INSERT OR IGNORE INTO transactions
				(bank_transaction_id, account_id, date, amount_cents, currency,
				 merchant_name, transfer_title, creditor_iban, debtor_iban,
				 credit_debit_indicator,
				 category, tags, notes, needs_review)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				t.TransactionID(), accountID, t.BookingDate, amountCents, t.TransactionAmount.Currency,
				t.MerchantName(), transferTitle, creditorIBAN, debtorIBAN,
				t.CreditDebitIndicator,
				category, tagsJSON, nil, 0,
			)
			if err != nil {
				return nil, nil, fmt.Errorf("inserting transaction %s: %w", t.TransactionID(), err)
			}

			inserted, err := res.RowsAffected()
			if err != nil {
				return nil, nil, fmt.Errorf("checking insert for transaction %s: %w", t.TransactionID(), err)
			}

			result.Ingested++

			if inserted == 1 && isSalary && category == categorySalary {
				rolled, err := s.RollPayPeriod(tx, bookDate, cfg.SalaryMinGapDays)
				if err != nil {
					return nil, nil, fmt.Errorf("rolling pay period: %w", err)
				}
				if rolled {
					result.PayPeriodRolled = true
				}
			}
		}

		allTxs = append(allTxs, accountTxs...)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("committing transaction: %w", err)
	}

	return allTxs, result, nil
}
