package service

import (
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

const hoursPerDay = 24

// The employer has changed separators, casing and field order over time, so
// month-first and year-first forms are matched separately.

// listaPlacMonthFirstRe matches month-first, four-digit year titles such as
// "Lista Plac 09/2026", "09 2026", "09.2026", "09-2026".
var listaPlacMonthFirstRe = regexp.MustCompile(`(?i)\blista\s+plac\s+\d{2}[/.\- ]\d{4}\b`)

// listaPlacYearFirstRe matches year-first, two-digit month titles such as
// "LISTA PLAC 2026/09".
var listaPlacYearFirstRe = regexp.MustCompile(`(?i)\blista\s+plac\s+\d{4}[/.\- ]\d{2}\b`)

// IsListaPlac reports whether the transfer title identifies a "Lista Plac"
// (monthly salary) payment.
func IsListaPlac(title string) bool {
	return listaPlacMonthFirstRe.MatchString(title) || listaPlacYearFirstRe.MatchString(title)
}

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
	startDate := salaryDate.Format("2006-01-02")

	// Idempotency: a period already starting on this salary date means this
	// salary has already been rolled (e.g. a historical replay). Do nothing.
	var existing int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM pay_periods WHERE start_date = ?",
		startDate,
	).Scan(&existing); err != nil {
		return false, fmt.Errorf("checking existing period: %w", err)
	}
	if existing > 0 {
		return false, nil
	}

	var lastSalaryDate *string
	err := tx.QueryRow(
		`SELECT date FROM transactions WHERE category = 'Salary' AND date < ? ORDER BY date DESC LIMIT 1`,
		startDate,
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

	// Only close periods that started before this salary. The `start_date <= ?`
	// guard guarantees a closed period never ends before it starts.
	closeDate := salaryDate.AddDate(0, 0, -1).Format("2006-01-02")
	if _, err := tx.Exec(
		"UPDATE pay_periods SET end_date = ? WHERE end_date IS NULL AND start_date < ? AND start_date <= ?",
		closeDate, startDate, closeDate,
	); err != nil {
		return false, fmt.Errorf("closing open period: %w", err)
	}

	if _, err := tx.Exec(
		"INSERT INTO pay_periods (start_date) VALUES (?)",
		startDate,
	); err != nil {
		return false, fmt.Errorf("opening new period: %w", err)
	}

	return true, nil
}
