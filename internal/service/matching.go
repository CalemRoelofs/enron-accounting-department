package service

import (
	"database/sql"

	"github.com/calemroelofs/enron-accounting-department/internal/config"
)

const minMatchCount = 2

// MatchInterAccountTransfer checks if both IBANs match known accounts.
//
//nolint:noctx // deliberate: QueryRow uses background context implicitly
func (s *Service) MatchInterAccountTransfer(tx *sql.Tx, creditorIBAN, debtorIBAN string) (bool, error) {
	if creditorIBAN == "" && debtorIBAN == "" {
		return false, nil
	}
	var count int
	err := tx.QueryRow(
		"SELECT COUNT(*) FROM accounts WHERE iban IN (?, ?)",
		creditorIBAN, debtorIBAN,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count >= minMatchCount, nil
}

// CounterpartyMatch holds the result of a counterparty match.
type CounterpartyMatch struct {
	Matched  bool
	Label    string
	Category string
}

// MatchCounterparty matches a creditor or debtor IBAN against known counterparties.
func MatchCounterparty(creditorIBAN, debtorIBAN string, counterparties []config.KnownCounterparty) *CounterpartyMatch {
	for _, cp := range counterparties {
		if creditorIBAN == cp.IBAN || debtorIBAN == cp.IBAN {
			return &CounterpartyMatch{
				Matched:  true,
				Label:    cp.Label,
				Category: cp.Category,
			}
		}
	}
	return &CounterpartyMatch{Matched: false}
}
