package service

import (
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

const hoursPerDay = 24

// ListaPlacRe matches "Lista Plac" salary transfer titles.
var ListaPlacRe = regexp.MustCompile(`(?i)Lista Plac \d{2}/\d{4}`)

// DetectSalary checks if a transfer is a salary payment.
func DetectSalary(amountValue float64, debtorIBAN, _ /* transferTitle */, employerIBAN string) bool {
	if amountValue <= 0 {
		return false
	}
	if debtorIBAN != employerIBAN {
		return false
	}
	return true
}

// RollPayPeriod rolls the pay period when a new salary is detected.
//
//nolint:govet,noctx // err shadowing and context use within function are by design
func (s *Service) RollPayPeriod(tx *sql.Tx, salaryDate time.Time, minGapDays int) (bool, error) {
	var lastSalaryDate *string
	err := tx.QueryRow(
		`SELECT date FROM transactions WHERE category = 'Salary' AND date < ? ORDER BY date DESC LIMIT 1`,
		salaryDate.Format("2006-01-02"),
	).Scan(&lastSalaryDate)
	if err != nil && err != sql.ErrNoRows {
		return false, fmt.Errorf("querying last salary: %w", err)
	}

	if lastSalaryDate != nil {
		parsed, err := time.Parse("2006-01-02", *lastSalaryDate)
		if err != nil {
			return false, fmt.Errorf("parsing last salary date: %w", err)
		}
		gap := int(salaryDate.Sub(parsed).Hours() / hoursPerDay)
		if gap < minGapDays {
			return false, nil
		}
	}

	var openPeriodCount int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM pay_periods WHERE end_date IS NULL",
	).Scan(&openPeriodCount); err != nil {
		return false, fmt.Errorf("counting open periods: %w", err)
	}

	if openPeriodCount > 0 {
		closeDate := salaryDate.AddDate(0, 0, -1).Format("2006-01-02")
		if _, err := tx.Exec(
			"UPDATE pay_periods SET end_date = ? WHERE end_date IS NULL",
			closeDate,
		); err != nil {
			return false, fmt.Errorf("closing open period: %w", err)
		}
	}

	startDate := salaryDate.Format("2006-01-02")
	if _, err := tx.Exec(
		"INSERT INTO pay_periods (start_date) VALUES (?)",
		startDate,
	); err != nil {
		return false, fmt.Errorf("opening new period: %w", err)
	}

	return true, nil
}
