//nolint:govet,paralleltest // deep err shadowing; subtests not parallelized for shared DB state
package service_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/calemroelofs/enron-accounting-department/internal/config"
	"github.com/calemroelofs/enron-accounting-department/internal/db"
	"github.com/calemroelofs/enron-accounting-department/internal/enablebanking"
	"github.com/calemroelofs/enron-accounting-department/internal/service"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbase, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init test db: %v", err)
	}
	return dbase
}

func seedAccount(t *testing.T, dbase *sql.DB, name, iban string) int64 {
	t.Helper()
	res, err := dbase.Exec("INSERT INTO accounts (name, iban) VALUES (?, ?)", name, iban)
	if err != nil {
		t.Fatalf("seeding account: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

//nolint:paralleltest,tparallel,gocognit // subtests share parent's database; complexity inherent to test
func TestMatchInterAccountTransfer(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	millIBAN := "PL987654321098765432109876"
	revIBAN := "LT304580906123456789"
	seedAccount(t, dbase, "Millennium", millIBAN)
	seedAccount(t, dbase, "Revolut", revIBAN)

	t.Run("both ibans match accounts", func(t *testing.T) {
		tx, err := dbase.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()

		matched, err := svc.MatchInterAccountTransfer(tx, revIBAN, millIBAN)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !matched {
			t.Error("expected transfer match when both IBANs are known accounts")
		}
	})

	t.Run("only one iban matches", func(t *testing.T) {
		tx, err := dbase.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()

		matched, err := svc.MatchInterAccountTransfer(tx, revIBAN, "PL000000000000000000000000")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched {
			t.Error("expected no match when only one IBAN is a known account")
		}
	})

	t.Run("neither iban matches", func(t *testing.T) {
		tx, err := dbase.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()

		matched, err := svc.MatchInterAccountTransfer(tx, "PL111111111111111111111111", "PL222222222222222222222222")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched {
			t.Error("expected no match when neither IBAN is a known account")
		}
	})

	t.Run("empty ibans", func(t *testing.T) {
		tx, err := dbase.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()

		matched, err := svc.MatchInterAccountTransfer(tx, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if matched {
			t.Error("expected no match for empty IBANs")
		}
	})
}

func TestMatchCounterparty(t *testing.T) {
	t.Parallel()
	counterparties := []config.KnownCounterparty{
		{IBAN: "DE12345678901234567890", Label: "Trade Republic", Category: "Savings_TR"},
		{IBAN: "PL111122223333444455556666", Label: "IKE", Category: "Savings_IKE"},
	}

	t.Run("creditor iban matches", func(t *testing.T) {
		t.Parallel()
		match := service.MatchCounterparty("DE12345678901234567890", "PL987654321098765432109876", counterparties)
		if !match.Matched {
			t.Error("expected match")
		}
		if match.Category != "Savings_TR" {
			t.Errorf("expected Savings_TR, got %s", match.Category)
		}
	})

	t.Run("debtor iban matches", func(t *testing.T) {
		t.Parallel()
		match := service.MatchCounterparty("PL987654321098765432109876", "DE12345678901234567890", counterparties)
		if !match.Matched {
			t.Error("expected match")
		}
		if match.Category != "Savings_TR" {
			t.Errorf("expected Savings_TR, got %s", match.Category)
		}
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		match := service.MatchCounterparty("PL000000000000000000000000", "PL111111111111111111111111", counterparties)
		if match.Matched {
			t.Error("expected no match")
		}
	})

	t.Run("empty counterparties list", func(t *testing.T) {
		t.Parallel()
		match := service.MatchCounterparty("DE12345678901234567890", "", nil)
		if match.Matched {
			t.Error("expected no match with empty list")
		}
	})
}

func TestDetectSalary(t *testing.T) {
	t.Parallel()
	employerIBAN := "PL541140100000200301001002"

	t.Run("credit from employer iban", func(t *testing.T) {
		t.Parallel()
		got := service.DetectSalary(15319.93, employerIBAN, "Lista Plac 08/2026", employerIBAN)
		if !got {
			t.Error("expected salary detection")
		}
	})

	t.Run("debit from employer should not match", func(t *testing.T) {
		t.Parallel()
		got := service.DetectSalary(-500.00, employerIBAN, "Lista Plac 08/2026", employerIBAN)
		if got {
			t.Error("expected no salary detection for debit")
		}
	})

	t.Run("wrong debtor iban", func(t *testing.T) {
		t.Parallel()
		got := service.DetectSalary(15319.93, "PL000000000000000000000000", "Lista Plac 08/2026", employerIBAN)
		if got {
			t.Error("expected no salary detection for wrong debtor")
		}
	})

	t.Run("zero amount", func(t *testing.T) {
		t.Parallel()
		got := service.DetectSalary(0, employerIBAN, "Lista Plac 08/2026", employerIBAN)
		if got {
			t.Error("expected no salary detection for zero amount")
		}
	})
}

//nolint:paralleltest,tparallel,gocognit // subtests share parent's database; complexity inherent to test
func TestRollPayPeriod(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	t.Run("first salary opens period", func(t *testing.T) {
		tx, err := dbase.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()

		date := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
		rolled, err := svc.RollPayPeriod(tx, date, 25)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !rolled {
			t.Error("expected period to be rolled")
		}

		var count int
		if err := tx.QueryRow("SELECT COUNT(*) FROM pay_periods").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("expected 1 pay period, got %d", count)
		}

		var startDate string
		var endDate *string
		if err := tx.QueryRow("SELECT start_date, end_date FROM pay_periods").Scan(&startDate, &endDate); err != nil {
			t.Fatal(err)
		}
		if startDate != "2026-08-27" {
			t.Errorf("expected start 2026-08-27, got %s", startDate)
		}
		if endDate != nil {
			t.Errorf("expected nil end_date for open period, got %v", *endDate)
		}

		_ = tx.Rollback()
	})

	t.Run("second salary within gap does not roll", func(t *testing.T) {
		_, _ = dbase.Exec("INSERT INTO pay_periods (start_date) VALUES ('2026-07-28')")
		_, _ = dbase.Exec(
			"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency, creditor_iban, debtor_iban, category) VALUES ('S1', 0, '2026-07-28', 1531993, 'PLN', '', 'PL541140100000200301001002', 'Salary')",
		)

		tx, err := dbase.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()

		date := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
		rolled, err := svc.RollPayPeriod(tx, date, 25)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rolled {
			t.Error("expected no roll when within gap")
		}

		_ = tx.Rollback()
	})

	t.Run("second salary after gap rolls", func(t *testing.T) {
		_, _ = dbase.Exec("DELETE FROM pay_periods")
		_, _ = dbase.Exec("DELETE FROM transactions")
		_, _ = dbase.Exec("INSERT INTO pay_periods (start_date) VALUES ('2026-07-20')")
		_, _ = dbase.Exec(
			"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency, creditor_iban, debtor_iban, category) VALUES ('S2', 0, '2026-07-20', 1531993, 'PLN', '', 'PL541140100000200301001002', 'Salary')",
		)

		tx, err := dbase.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()

		date := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
		rolled, err := svc.RollPayPeriod(tx, date, 25)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !rolled {
			t.Error("expected roll when beyond gap")
		}

		var periods int
		_ = tx.QueryRow("SELECT COUNT(*) FROM pay_periods").Scan(&periods)
		if periods != 2 {
			t.Errorf("expected 2 periods, got %d", periods)
		}

		var oldEnd *string
		_ = tx.QueryRow("SELECT end_date FROM pay_periods WHERE start_date = '2026-07-20'").Scan(&oldEnd)
		if oldEnd == nil || *oldEnd != "2026-08-26" {
			t.Errorf("expected old period to be closed at 2026-08-26, got %v", oldEnd)
		}

		_ = tx.Rollback()
	})
}

func seedSalaryTransactions(t *testing.T, dbase *sql.DB, dates ...string) {
	t.Helper()
	for _, d := range dates {
		_, err := dbase.Exec(
			"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency, creditor_iban, debtor_iban, category) VALUES (?, 0, ?, 1531993, 'PLN', '', 'PL541140100000200301001002', 'Salary')",
			"sal-"+d,
			d,
		)
		if err != nil {
			t.Fatalf("seeding salary %s: %v", d, err)
		}
	}
}

func rollSalaryDates(t *testing.T, svc *service.Service, dbase *sql.DB, dates ...string) {
	t.Helper()
	for _, d := range dates {
		date, err := time.Parse("2006-01-02", d)
		if err != nil {
			t.Fatalf("parsing date %s: %v", d, err)
		}
		tx, err := dbase.Begin()
		if err != nil {
			t.Fatalf("beginning tx: %v", err)
		}
		if _, err := svc.RollPayPeriod(tx, date, 25); err != nil {
			t.Fatalf("rolling %s: %v", d, err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatalf("committing %s: %v", d, err)
		}
	}
}

//nolint:paralleltest // subtests share parent's database
func TestRollPayPeriod_Idempotent(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dates := []string{"2026-06-29", "2026-07-30", "2026-08-27"}
	seedSalaryTransactions(t, dbase, dates...)

	for range 3 {
		rollSalaryDates(t, svc, dbase, dates...)
	}

	type period struct {
		start string
		end   *string
	}
	rows, err := dbase.Query("SELECT start_date, end_date FROM pay_periods ORDER BY start_date")
	if err != nil {
		t.Fatalf("querying periods: %v", err)
	}
	defer rows.Close()

	var got []period
	for rows.Next() {
		var p period
		if err := rows.Scan(&p.start, &p.end); err != nil {
			t.Fatalf("scanning period: %v", err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating periods: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected exactly 3 periods after 3 syncs, got %d", len(got))
	}

	want := []period{
		{start: "2026-06-29", end: new("2026-07-29")},
		{start: "2026-07-30", end: new("2026-08-26")},
		{start: "2026-08-27", end: nil},
	}
	for i, w := range want {
		if got[i].start != w.start {
			t.Errorf("period %d: expected start %s, got %s", i, w.start, got[i].start)
		}
		switch {
		case got[i].end == nil && w.end == nil:
		case got[i].end == nil || w.end == nil:
			t.Errorf("period %d (%s): expected end %v, got %v", i, w.start, w.end, got[i].end)
		case *got[i].end != *w.end:
			t.Errorf("period %d (%s): expected end %s, got %s", i, w.start, *w.end, *got[i].end)
		}
	}
}

//nolint:paralleltest // subtests share parent's database
func TestRollPayPeriod_NeverEndsBeforeStart(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dates := []string{"2026-06-29", "2026-07-30", "2026-08-27"}
	seedSalaryTransactions(t, dbase, dates...)
	for range 3 {
		rollSalaryDates(t, svc, dbase, dates...)
	}

	var violations int
	if err := dbase.QueryRow(
		"SELECT COUNT(*) FROM pay_periods WHERE end_date IS NOT NULL AND end_date < start_date",
	).Scan(&violations); err != nil {
		t.Fatalf("counting violations: %v", err)
	}
	if violations != 0 {
		t.Errorf("expected no period to end before it starts, got %d violations", violations)
	}
}

//nolint:paralleltest // subtests share parent's database
func TestRollPayPeriod_OlderSalaryKeepsNewerOpenPeriod(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	seedSalaryTransactions(t, dbase, "2026-06-29", "2026-08-27")
	rollSalaryDates(t, svc, dbase, "2026-08-27")
	rollSalaryDates(t, svc, dbase, "2026-06-29")

	var endDate *string
	if err := dbase.QueryRow(
		"SELECT end_date FROM pay_periods WHERE start_date = '2026-08-27'",
	).Scan(&endDate); err != nil {
		t.Fatalf("querying newer period: %v", err)
	}
	if endDate != nil {
		t.Errorf("re-rolling an older salary mutated the newer open period: end_date = %s", *endDate)
	}
}

//nolint:paralleltest,tparallel // subtests share parent's database
func TestAddLineItem(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	millID := seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")
	_, _ = dbase.Exec(
		"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency, creditor_iban, debtor_iban) VALUES ('T1', ?, '2026-09-01', 10000, 'PLN', '', '')",
		millID,
	)

	t.Run("add first line item", func(t *testing.T) {
		result, err := svc.AddLineItem(1, "milk", 5000, "", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Errorf("expected success, got %s", result.Status)
		}
		if result.RunningTotalCents != 5000 {
			t.Errorf("expected running total 5000, got %d", result.RunningTotalCents)
		}
		if result.TransactionTotalCents != 10000 {
			t.Errorf("expected tx total 10000, got %d", result.TransactionTotalCents)
		}
		if result.RemainingCents != 5000 {
			t.Errorf("expected remaining 5000, got %d", result.RemainingCents)
		}
	})

	t.Run("add second line item", func(t *testing.T) {
		result, err := svc.AddLineItem(1, "bread", 3000, "", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RunningTotalCents != 8000 {
			t.Errorf("expected running total 8000, got %d", result.RunningTotalCents)
		}
		if result.RemainingCents != 2000 {
			t.Errorf("expected remaining 2000, got %d", result.RemainingCents)
		}
	})

	t.Run("transaction not found", func(t *testing.T) {
		_, err := svc.AddLineItem(999, "test", 100, "", nil, nil)
		if err == nil {
			t.Error("expected error for non-existent transaction")
		}
	})
}

//nolint:paralleltest,tparallel // subtests share parent's database
func TestCheckLineItem(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	millID := seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")
	_, _ = dbase.Exec(
		"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency, creditor_iban, debtor_iban) VALUES ('T1', ?, '2026-09-01', 10000, 'PLN', '', '')",
		millID,
	)

	t.Run("sum matches", func(t *testing.T) {
		_, _ = svc.AddLineItem(1, "milk", 5000, "", nil, nil)
		_, _ = svc.AddLineItem(1, "bread", 5000, "", nil, nil)

		result, err := svc.CheckLineItem(1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.DiscrepancyCents != 0 {
			t.Errorf("expected 0 discrepancy, got %d", result.DiscrepancyCents)
		}
		if result.Warning != "" {
			t.Errorf("expected no warning, got %s", result.Warning)
		}
	})

	t.Run("sum mismatch", func(t *testing.T) {
		_, _ = dbase.Exec("DELETE FROM line_items")
		_, _ = svc.AddLineItem(1, "milk", 4000, "", nil, nil)
		_, _ = svc.AddLineItem(1, "bread", 2000, "", nil, nil)

		result, err := svc.CheckLineItem(1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.DiscrepancyCents != 4000 {
			t.Errorf("expected 4000 discrepancy, got %d", result.DiscrepancyCents)
		}
		if result.Warning == "" {
			t.Error("expected warning for discrepancy")
		}
	})

	t.Run("no line items", func(t *testing.T) {
		_, _ = dbase.Exec("DELETE FROM line_items")
		result, err := svc.CheckLineItem(1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.DiscrepancyCents != 10000 {
			t.Errorf("expected 10000 discrepancy for no line items, got %d", result.DiscrepancyCents)
		}
	})

	t.Run("transaction not found", func(t *testing.T) {
		_, err := svc.CheckLineItem(999)
		if err == nil {
			t.Error("expected error for non-existent transaction")
		}
	})
}

func TestGuardSQL(t *testing.T) {
	t.Parallel()
	t.Run("valid select", func(t *testing.T) {
		t.Parallel()
		//nolint:unqueryvet // intentional SELECT * for GuardSQL tests
		if err := service.GuardSQL("SELECT * FROM transactions"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid select with where", func(t *testing.T) {
		t.Parallel()
		if err := service.GuardSQL("SELECT id, amount_cents FROM transactions WHERE category = 'Salary'"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty query", func(t *testing.T) {
		t.Parallel()
		if err := service.GuardSQL(""); err == nil {
			t.Error("expected error for empty query")
		}
	})

	t.Run("starts with insert", func(t *testing.T) {
		t.Parallel()
		if err := service.GuardSQL("INSERT INTO transactions VALUES (1)"); err == nil {
			t.Error("expected error for INSERT")
		}
	})

	t.Run("semicolon batch", func(t *testing.T) {
		t.Parallel()
		//nolint:unqueryvet // intentional SELECT * for GuardSQL tests
		if err := service.GuardSQL("SELECT * FROM transactions; SELECT * FROM accounts"); err == nil {
			t.Error("expected error for semicolon batch")
		}
	})

	t.Run("pragma", func(t *testing.T) {
		t.Parallel()
		if err := service.GuardSQL("PRAGMA journal_mode=WAL"); err == nil {
			t.Error("expected error for PRAGMA")
		}
	})

	t.Run("attach", func(t *testing.T) {
		t.Parallel()
		if err := service.GuardSQL("ATTACH DATABASE 'file.db' AS x"); err == nil {
			t.Error("expected error for ATTACH")
		}
	})

	t.Run("vacuum", func(t *testing.T) {
		t.Parallel()
		if err := service.GuardSQL("VACUUM"); err == nil {
			t.Error("expected error for VACUUM")
		}
	})

	t.Run("select with leading whitespace", func(t *testing.T) {
		t.Parallel()
		//nolint:unqueryvet // intentional SELECT * for GuardSQL tests
		if err := service.GuardSQL("  SELECT * FROM transactions"); err != nil {
			t.Errorf("expected valid, got: %v", err)
		}
	})

	t.Run("drop table", func(t *testing.T) {
		t.Parallel()
		if err := service.GuardSQL("DROP TABLE transactions"); err == nil {
			t.Error("expected error for DROP")
		}
	})
}

//nolint:paralleltest,tparallel // subtests share parent's database
func TestExecuteQuery(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"
	dbase, err := db.InitDB(dbPath)
	if err != nil {
		t.Fatalf("init test db: %v", err)
	}

	svc := service.NewService(dbase)

	millID := seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")
	_, _ = dbase.Exec(
		"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency, creditor_iban, debtor_iban, category) VALUES ('S1', ?, '2026-08-27', 1531993, 'PLN', '', 'PL541140100000200301001002', 'Salary')",
		millID,
	)
	dbase.Close()

	t.Run("select all transactions", func(t *testing.T) {
		result, err := svc.ExecuteQuery(dbPath, "SELECT id, category, amount_cents FROM transactions ORDER BY id")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var rows []map[string]any
		if err := json.Unmarshal([]byte(result), &rows); err != nil {
			t.Fatalf("unmarshaling result: %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("expected 1 row, got %d", len(rows))
		}
		if rows[0]["category"] != "Salary" {
			t.Errorf("expected Salary category, got %v", rows[0]["category"])
		}
	})

	t.Run("invalid sql rejected by guard", func(t *testing.T) {
		_, err := svc.ExecuteQuery(dbPath, "DROP TABLE transactions")
		if err == nil {
			t.Error("expected error for DROP query")
		}
	})

	t.Run("select from empty table", func(t *testing.T) {
		result, err := svc.ExecuteQuery(dbPath, "SELECT id, start_date, end_date FROM pay_periods")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "[]" {
			t.Errorf("expected empty array, got %s", result)
		}
	})
}

func TestSyncFromFixture(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")
	seedAccount(t, dbase, "Revolut", "LT304580906123456789")

	cfg := &config.Config{
		EmployerIBAN:     "PL541140100000200301001002",
		SalaryMinGapDays: 25,
		KnownCounterparties: []config.KnownCounterparty{
			{IBAN: "DE12345678901234567890", Label: "Trade Republic", Category: "Savings_TR"},
		},
	}

	fixture := `{
		"transactions": [
			{
				"entry_reference": "20260905-MILL-TR-99382",
				"booking_date": "2026-09-05",
				"value_date": "2026-09-05",
				"transaction_amount": {"amount": "2000.00", "currency": "PLN"},
				"creditor": {"name": "JOHN DOE"},
				"creditor_account": {"iban": "DE12345678901234567890"},
				"debtor_account": {"iban": "PL987654321098765432109876"},
				"remittance_information": ["Trade Republic Deposit"],
				"credit_debit_indicator": "DBIT",
				"status": "BOOKED"
			},
			{
				"entry_reference": "20260903-MILL-REV-11223",
				"booking_date": "2026-09-03",
				"value_date": "2026-09-03",
				"transaction_amount": {"amount": "500.00", "currency": "PLN"},
				"creditor": {"name": "JOHN DOE"},
				"creditor_account": {"iban": "LT304580906123456789"},
				"debtor_account": {"iban": "PL987654321098765432109876"},
				"remittance_information": ["Top up"],
				"credit_debit_indicator": "DBIT",
				"status": "BOOKED"
			},
			{
				"entry_reference": "20260827-MILL-SALARY-00417",
				"booking_date": "2026-08-27",
				"value_date": "2026-08-27",
				"transaction_amount": {"amount": "15319.93", "currency": "PLN"},
				"creditor": {"name": "JOHN DOE"},
				"creditor_account": {"iban": "PL987654321098765432109876"},
				"debtor_account": {"iban": "PL541140100000200301001002"},
				"remittance_information": ["Lista Plac 08/2026"],
				"credit_debit_indicator": "CRDT",
				"status": "BOOKED"
			}
		],
		"continuation_key": null
	}`

	result, err := svc.SyncFromFixture(cfg, []byte(fixture))
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	if result.Ingested != 3 {
		t.Errorf("expected 3 ingested, got %d", result.Ingested)
	}
	if result.TransfersMatched != 1 {
		t.Errorf("expected 1 transfer matched (Millennium->Revolut), got %d", result.TransfersMatched)
	}
	if !result.PayPeriodRolled {
		t.Error("expected pay period to be rolled for salary")
	}

	var txCount int
	_ = dbase.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&txCount)
	if txCount != 3 {
		t.Errorf("expected 3 transactions in db, got %d", txCount)
	}

	var categories []string
	rows, qErr := dbase.Query("SELECT category FROM transactions ORDER BY id")
	if qErr != nil {
		t.Fatalf("querying categories: %v", qErr)
	}
	defer rows.Close()
	for rows.Next() {
		var cat string
		if err := rows.Scan(&cat); err != nil {
			t.Fatal(err)
		}
		categories = append(categories, cat)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	expectedCats := []string{"Salary", "Transfer_Internal", "Savings_TR"}
	for i, expected := range expectedCats {
		if categories[i] != expected {
			t.Errorf("transaction %d: expected category %s, got %s", i+1, expected, categories[i])
		}
	}

	var periodCount int
	_ = dbase.QueryRow("SELECT COUNT(*) FROM pay_periods").Scan(&periodCount)
	if periodCount != 1 {
		t.Errorf("expected 1 pay period, got %d", periodCount)
	}
}

func TestSyncFromFixture_SaveAndReplay(t *testing.T) {
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")

	cfg := &config.Config{
		EmployerIBAN:     "PL541140100000200301001002",
		SalaryMinGapDays: 25,
	}

	fixtureJSON := `{
		"transactions": [{
			"entry_reference": "20260827-MILL-SALARY-00417",
			"booking_date": "2026-08-27",
			"value_date": "2026-08-27",
			"transaction_amount": {"amount": "15319.93", "currency": "PLN"},
			"creditor": {"name": "JOHN DOE"},
			"creditor_account": {"iban": "PL987654321098765432109876"},
			"debtor_account": {"iban": "PL541140100000200301001002"},
			"remittance_information": ["Lista Plac 08/2026"],
			"credit_debit_indicator": "CRDT",
			"status": "BOOKED"
		}],
		"continuation_key": null
	}`

	result, err := svc.SyncFromFixture(cfg, []byte(fixtureJSON))
	if err != nil {
		t.Fatalf("SyncFromFixture failed: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("expected 1 ingested, got %d", result.Ingested)
	}
	if !result.PayPeriodRolled {
		t.Error("expected pay period to be rolled for salary")
	}
}

//nolint:paralleltest // subtests share parent's database
func TestSyncFromFixture_NegatesDebit(t *testing.T) {
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")

	cfg := &config.Config{
		EmployerIBAN:     "PL541140100000200301001002",
		SalaryMinGapDays: 25,
	}

	fixture := `{
		"transactions": [
			{
				"entry_reference": "DBIT-TEST-001",
				"booking_date": "2026-09-01",
				"value_date": "2026-09-01",
				"transaction_amount": {"amount": "500.00", "currency": "PLN"},
				"creditor": {"name": "ALDI"},
				"creditor_account": {"iban": "DE44444444444444444444"},
				"debtor_account": {"iban": "PL987654321098765432109876"},
				"remittance_information": ["Groceries"],
				"credit_debit_indicator": "DBIT",
				"status": "BOOKED"
			},
			{
				"entry_reference": "CRDT-TEST-001",
				"booking_date": "2026-09-01",
				"value_date": "2026-09-01",
				"transaction_amount": {"amount": "1000.00", "currency": "PLN"},
				"creditor": {"name": "JOHN DOE"},
				"creditor_account": {"iban": "PL987654321098765432109876"},
				"debtor_account": {"iban": "PL55555555555555555555"},
				"remittance_information": ["Refund"],
				"credit_debit_indicator": "CRDT",
				"status": "BOOKED"
			}
		],
		"continuation_key": null
	}`

	result, err := svc.SyncFromFixture(cfg, []byte(fixture))
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if result.Ingested != 2 {
		t.Errorf("expected 2 ingested, got %d", result.Ingested)
	}

	rows, qErr := dbase.Query(
		"SELECT bank_transaction_id, amount_cents, credit_debit_indicator FROM transactions ORDER BY bank_transaction_id",
	)
	if qErr != nil {
		t.Fatalf("query failed: %v", qErr)
	}
	defer rows.Close()

	for rows.Next() {
		var ref string
		var cents int64
		var indicator string
		if err := rows.Scan(&ref, &cents, &indicator); err != nil {
			t.Fatalf("scan failed: %v", err)
		}
		if indicator == "DBIT" && cents >= 0 {
			t.Errorf("expected negative amount_cents for DBIT %s, got %d", ref, cents)
		}
		if indicator == "CRDT" && cents <= 0 {
			t.Errorf("expected positive amount_cents for CRDT %s, got %d", ref, cents)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

type fakeTransactionFetcher struct {
	results map[string][]enablebanking.Transaction
	calls   []string
}

func (f *fakeTransactionFetcher) GetTransactions(
	accountID, _ string,
	_ time.Time,
) ([]enablebanking.Transaction, *string, error) {
	f.calls = append(f.calls, accountID)
	if accountID == "" {
		return nil, nil, errors.New("status 404")
	}
	return f.results[accountID], nil, nil
}

func TestSyncFromAPI_SkipsAccountsWithoutExternalID(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	validIBAN := "PL987654321098765432109876"
	manualIBAN := "PL92116022020000000575810839"

	_, err := dbase.Exec(
		"INSERT INTO accounts (name, iban, external_id) VALUES (?, ?, ?)",
		"Millennium", validIBAN, "uid-valid",
	)
	if err != nil {
		t.Fatalf("seeding valid account: %v", err)
	}
	_, err = dbase.Exec(
		"INSERT INTO accounts (name, iban, external_id) VALUES (?, ?, ?)",
		"Manual", manualIBAN, "",
	)
	if err != nil {
		t.Fatalf("seeding manual account: %v", err)
	}

	fetcher := &fakeTransactionFetcher{
		results: map[string][]enablebanking.Transaction{
			"uid-valid": {{
				EntryReference:        "TX-VALID-1",
				BookingDate:           "2026-09-05",
				ValueDate:             "2026-09-05",
				TransactionAmount:     enablebanking.EnableBankingAmount{Value: "100.00", Currency: "PLN"},
				CreditorAccount:       enablebanking.EnableBankingAccount{IBANRaw: "DE44444444444444444444"},
				DebtorAccount:         enablebanking.EnableBankingAccount{IBANRaw: validIBAN},
				RemittanceInformation: []string{"Groceries"},
				CreditDebitIndicator:  "DBIT",
				Status:                "BOOKED",
			}},
		},
	}

	cfg := &config.Config{EmployerIBAN: "PL541140100000200301001002", SalaryMinGapDays: 25}

	allTxs, result, err := svc.SyncFromAPI(cfg, fetcher, 0)
	if err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	if result.Ingested != 1 {
		t.Errorf("expected 1 ingested, got %d", result.Ingested)
	}
	if len(allTxs) != 1 {
		t.Errorf("expected 1 transaction returned, got %d", len(allTxs))
	}
	if len(result.SkippedAccounts) != 1 || result.SkippedAccounts[0] != manualIBAN {
		t.Errorf("expected skipped accounts [%s], got %v", manualIBAN, result.SkippedAccounts)
	}
	if len(fetcher.calls) != 1 || fetcher.calls[0] != "uid-valid" {
		t.Errorf("expected fetcher called only for uid-valid, got %v", fetcher.calls)
	}

	var count int
	_ = dbase.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 transaction in db, got %d", count)
	}
}

//nolint:paralleltest,tparallel // subtests share parent's database
func TestCategorizeTransaction(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	millID := seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")
	_, _ = dbase.Exec(
		"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency, creditor_iban, debtor_iban) VALUES ('T1', ?, '2026-09-01', 5000, 'PLN', '', '')",
		millID,
	)

	t.Run("categorize with tags", func(t *testing.T) {
		notes := "test notes"
		err := svc.CategorizeTransaction(1, "Kids", []string{"toys", "fun"}, &notes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var category string
		var tagsJSON string
		var notesOut *string
		_ = dbase.QueryRow("SELECT category, tags, notes FROM transactions WHERE id = 1").
			Scan(&category, &tagsJSON, &notesOut)
		if category != "Kids" {
			t.Errorf("expected Kids, got %s", category)
		}
		if tagsJSON != `["toys","fun"]` {
			t.Errorf("expected [\"toys\",\"fun\"], got %s", tagsJSON)
		}
		if notesOut == nil || *notesOut != "test notes" {
			t.Errorf("expected test notes, got %v", notesOut)
		}
	})

	t.Run("categorize without tags or notes", func(t *testing.T) {
		err := svc.CategorizeTransaction(1, "Food", nil, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var category string
		_ = dbase.QueryRow("SELECT category FROM transactions WHERE id = 1").Scan(&category)
		if category != "Food" {
			t.Errorf("expected Food, got %s", category)
		}
	})

	t.Run("non-existent transaction", func(t *testing.T) {
		err := svc.CategorizeTransaction(999, "Test", nil, nil)
		if err == nil {
			t.Error("expected error for non-existent transaction")
		}
	})
}

func TestParseAmountCents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  int64
	}{
		{"-2000.00", -200000},
		{"15319.93", 1531993},
		{"0.00", 0},
		{"100.50", 10050},
		{"-0.01", -1},
	}

	parsed := make([]int64, len(tests))
	for i, tt := range tests {
		got, err := service.ParseAmountCents(tt.input)
		if err != nil {
			t.Errorf("unexpected error for %s: %v", tt.input, err)
		}
		parsed[i] = got
	}
	for i, tt := range tests {
		if parsed[i] != tt.want {
			t.Errorf("for %s: expected %d, got %d", tt.input, tt.want, parsed[i])
		}
	}
}

func TestListaPlacRegex(t *testing.T) {
	t.Parallel()
	matching := []string{
		"Lista Plac 08/2026",
		"lista plac 01/2025",
		"LISTA PLAC 12/2024",
		"Some prefix Lista Plac 06/2023 suffix",
	}
	nonMatching := []string{
		"Top up",
		"Trade Republic Deposit",
		"Lista Plac 08/26",
		"Lista 08/2026",
	}

	for _, s := range matching {
		if !service.ListaPlacRe.MatchString(s) {
			t.Errorf("expected match for %q", s)
		}
	}
	for _, s := range nonMatching {
		if service.ListaPlacRe.MatchString(s) {
			t.Errorf("expected no match for %q", s)
		}
	}
}

func TestNewService(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()

	svc := service.NewService(dbase)
	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.DB != dbase {
		t.Error("expected DB field to match")
	}
}

func TestSyncFromFixture_NoAccounts(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	cfg := &config.Config{
		EmployerIBAN:     "PL541140100000200301001002",
		SalaryMinGapDays: 25,
	}

	fixture := `{
		"transactions": [
			{
				"entry_reference": "tx-test",
				"booking_date": "2026-09-06",
				"value_date": "2026-09-06",
				"transaction_amount": {"amount": "-100.00", "currency": "PLN"},
				"creditor": {"name": "JOHN DOE"},
				"creditor_account": {"iban": "DE12345678901234567890"},
				"debtor_account": {"iban": "PL987654321098765432109876"},
				"credit_debit_indicator": "DBIT",
				"status": "BOOKED"
			}
		],
		"continuation_key": null
	}`

	_, err := svc.SyncFromFixture(cfg, []byte(fixture))
	if err == nil {
		t.Error("expected error when no accounts exist")
	}
}

//nolint:paralleltest // env var used for restore
func TestConfigPath(t *testing.T) {
	t.Setenv("HOME", "/tmp/testuser")

	path := config.ConfigPath()
	if path != "/tmp/testuser/.finance-cli/config.json" {
		t.Errorf("unexpected config path: %s", path)
	}
}

//nolint:paralleltest // subtests share parent's database
func TestCompleteAuthSession(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	key := generateTestKey(t)
	pemData := pemEncodePrivateKey(t, key)
	appID := "test-app-id"

	sessionResponse := `{
		"session_id":"sess_new",
		"accounts":[{"uid":"acc_1","iban":"PL123456789012345678901234","currency":"PLN","product":"Checking"}],
		"aspsp":{"name":"Nordea","country":"FI"},
		"psu_type":"personal",
		"access":{"valid_until":"2026-12-06T12:00:00Z"}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/sessions" {
			t.Errorf("expected /sessions, got %s", r.URL.Path)
		}
		// Verify body contains code
		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		if req.Code != "auth-code-123" {
			t.Errorf("expected code 'auth-code-123', got %q", req.Code)
		}
		_, _ = w.Write([]byte(sessionResponse))
	}))
	defer server.Close()

	ebClient, err := enablebanking.NewClient(appID, pemData, server.URL)
	if err != nil {
		t.Fatalf("creating EB client: %v", err)
	}

	// Insert a bank connection with the authorization_id as session_id
	_, err = dbase.Exec(
		"INSERT INTO bank_connections (provider, session_id, aspsp_id, consent_granted_at, consent_expires_at) VALUES (?, ?, ?, ?, ?)",
		"enablebanking",
		"sess_abc", // authorization_id
		"bank1",
		"2026-09-06T10:00:00Z",
		"2026-09-06T10:00:00Z",
	)
	if err != nil {
		t.Fatalf("inserting initial bank connection: %v", err)
	}

	// Complete the session
	sessionID, consentExpiresAt, err := svc.CompleteAuthSession(ebClient, "auth-code-123", "sess_abc")
	if err != nil {
		t.Fatalf("CompleteAuthSession failed: %v", err)
	}
	if sessionID != "sess_new" {
		t.Errorf("expected session_id 'sess_new', got %q", sessionID)
	}
	if consentExpiresAt == "" {
		t.Error("expected non-empty consent_expires_at")
	}

	// Verify the bank_connections row was updated - look up by the new session_id
	var expiresAt string
	err = dbase.QueryRow("SELECT consent_expires_at FROM bank_connections WHERE session_id = ?", "sess_new").
		Scan(&expiresAt)
	if err != nil {
		t.Fatalf("querying bank_connections: %v", err)
	}
	if expiresAt != "2026-12-06T12:00:00Z" {
		t.Errorf("expected consent_expires_at '2026-12-06T12:00:00Z', got %q", expiresAt)
	}
}

//nolint:paralleltest // subtests share parent's database
func TestListBankConnections(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	// Insert a couple of connections
	_, err := dbase.Exec(
		"INSERT INTO bank_connections (provider, session_id, aspsp_id, consent_granted_at, consent_expires_at) VALUES (?, ?, ?, ?, ?)",
		"enablebanking",
		"sess_1",
		"bank1",
		"2026-09-01T10:00:00Z",
		"2026-12-01T10:00:00Z",
	)
	if err != nil {
		t.Fatalf("inserting first connection: %v", err)
	}
	_, err = dbase.Exec(
		"INSERT INTO bank_connections (provider, session_id, aspsp_id, consent_granted_at, consent_expires_at) VALUES (?, ?, ?, ?, ?)",
		"enablebanking",
		"sess_2",
		"bank2",
		"2026-09-02T10:00:00Z",
		"2026-12-02T10:00:00Z",
	)
	if err != nil {
		t.Fatalf("inserting second connection: %v", err)
	}

	connections, err := svc.ListBankConnections()
	if err != nil {
		t.Fatalf("ListBankConnections failed: %v", err)
	}
	if len(connections) != 2 {
		t.Fatalf("expected 2 connections, got %d", len(connections))
	}
	if connections[0].SessionID != "sess_1" {
		t.Errorf("expected session_id 'sess_1', got %q", connections[0].SessionID)
	}
	if connections[1].SessionID != "sess_2" {
		t.Errorf("expected session_id 'sess_2', got %q", connections[1].SessionID)
	}
}

//nolint:paralleltest,gocognit,tparallel // subtests share parent's database; complexity inherent to test
func TestRemoveAccount(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	t.Run("success by iban", func(t *testing.T) {
		id := seedAccount(t, dbase, "Test", "PL92116022020000000575810839")
		defer func() { _, _ = dbase.Exec("DELETE FROM accounts WHERE id = ?", id) }()

		result, err := svc.RemoveAccount(0, "PL92116022020000000575810839", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Errorf("expected success, got %s", result.Status)
		}
		if result.RemovedAccountID != id {
			t.Errorf("expected removed id %d, got %d", id, result.RemovedAccountID)
		}
		if result.IBAN != "PL92116022020000000575810839" {
			t.Errorf("expected iban %s, got %s", "PL92116022020000000575810839", result.IBAN)
		}
		var count int
		_ = dbase.QueryRow("SELECT COUNT(*) FROM accounts WHERE id = ?", id).Scan(&count)
		if count != 0 {
			t.Error("expected account to be deleted")
		}
	})

	t.Run("refused when transactions reference account", func(t *testing.T) {
		id := seedAccount(t, dbase, "Test2", "PL92116022020000000575810840")
		_, _ = dbase.Exec(
			"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents) VALUES (?, ?, ?, ?)",
			"R1", id, "2026-01-01", 1000,
		)
		_, _ = dbase.Exec(
			"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents) VALUES (?, ?, ?, ?)",
			"R2", id, "2026-01-02", 2000,
		)
		defer func() {
			_, _ = dbase.Exec("DELETE FROM transactions WHERE account_id = ?", id)
			_, _ = dbase.Exec("DELETE FROM accounts WHERE id = ?", id)
		}()

		_, err := svc.RemoveAccount(0, "PL92116022020000000575810840", false)
		if err == nil {
			t.Fatal("expected error when transactions reference account")
		}
		if !strings.Contains(err.Error(), "2 transactions") {
			t.Errorf("expected error to mention '2 transactions', got: %v", err)
		}
	})

	t.Run("force removal with transactions", func(t *testing.T) {
		id := seedAccount(t, dbase, "Test3", "PL92116022020000000575810841")
		_, _ = dbase.Exec(
			"INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents) VALUES (?, ?, ?, ?)",
			"F1", id, "2026-01-01", 1000,
		)
		defer func() {
			_, _ = dbase.Exec("DELETE FROM transactions WHERE account_id = ?", id)
			_, _ = dbase.Exec("DELETE FROM accounts WHERE id = ?", id)
		}()

		result, err := svc.RemoveAccount(0, "PL92116022020000000575810841", true)
		if err != nil {
			t.Fatalf("unexpected error with force: %v", err)
		}
		if result.Status != "success" {
			t.Errorf("expected success, got %s", result.Status)
		}
		var count int
		_ = dbase.QueryRow("SELECT COUNT(*) FROM accounts WHERE id = ?", id).Scan(&count)
		if count != 0 {
			t.Error("expected account to be deleted with force")
		}
	})

	t.Run("not found by iban", func(t *testing.T) {
		_, err := svc.RemoveAccount(0, "PL000000000000000000000000", false)
		if err == nil {
			t.Fatal("expected error for non-existent iban")
		}
	})

	t.Run("success by id", func(t *testing.T) {
		id := seedAccount(t, dbase, "Test4", "PL92116022020000000575810842")
		defer func() { _, _ = dbase.Exec("DELETE FROM accounts WHERE id = ?", id) }()

		result, err := svc.RemoveAccount(id, "", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Status != "success" {
			t.Errorf("expected success, got %s", result.Status)
		}
		if result.RemovedAccountID != id {
			t.Errorf("expected removed id %d, got %d", id, result.RemovedAccountID)
		}
	})

	t.Run("not found by id", func(t *testing.T) {
		_, err := svc.RemoveAccount(999, "", false)
		if err == nil {
			t.Fatal("expected error for non-existent id")
		}
	})
}

// generateTestKey creates an RSA private key for testing.
func generateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	return key
}

// pemEncodePrivateKey encodes an RSA private key to PEM.
func pemEncodePrivateKey(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling private key: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return pem.EncodeToMemory(block)
}

func TestListBankConnections_Empty(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	connections, err := svc.ListBankConnections()
	if err != nil {
		t.Fatalf("ListBankConnections failed: %v", err)
	}
	if len(connections) != 0 {
		t.Errorf("expected 0 connections, got %d", len(connections))
	}
}
