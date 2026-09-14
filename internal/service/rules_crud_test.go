//nolint:govet,paralleltest,gocognit // err shadowing; subtests share parent's database; test complexity inherent
package service_test

import (
	"encoding/json"
	"testing"

	"github.com/calemroelofs/enron-accounting-department/internal/service"
)

func TestAddRule_RejectsInvalidField(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	_, err := svc.AddRule("invalid", "pattern", "Cat", "[]")
	if err == nil {
		t.Fatal("expected error for invalid field")
	}
}

func TestAddRule_RejectsEmptyPattern(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	_, err := svc.AddRule("title", "", "Cat", "[]")
	if err == nil {
		t.Fatal("expected error for empty pattern")
	}
}

func TestAddRule_RejectsEmptyCategory(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	_, err := svc.AddRule("title", "pattern", "", "[]")
	if err == nil {
		t.Fatal("expected error for empty category")
	}
}

func TestAddRule_TagsAreJSON(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	id, err := svc.AddRule("title", "BIEDRONKA", "Groceries", `["food","weekly"]`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero rule id")
	}

	var tags string
	err = dbase.QueryRow("SELECT tags FROM category_rules WHERE id = ?", id).Scan(&tags)
	if err != nil {
		t.Fatalf("querying rule: %v", err)
	}

	var parsed []string
	if err := json.Unmarshal([]byte(tags), &parsed); err != nil {
		t.Fatalf("expected valid JSON array, got %q: %v", tags, err)
	}
	if len(parsed) != 2 || parsed[0] != "food" || parsed[1] != "weekly" {
		t.Errorf("expected [food, weekly], got %v", parsed)
	}
}

func TestListRules_OrderedByID(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	_, _ = svc.AddRule("title", "B", "Cat2", "[]")
	_, _ = svc.AddRule("title", "A", "Cat1", "[]")
	_, _ = svc.AddRule("title", "C", "Cat3", "[]")

	rules, err := svc.ListRules()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(rules))
	}
	// Must be ordered by id (insertion order since autoincrement)
	if rules[0].ID >= rules[1].ID || rules[1].ID >= rules[2].ID {
		t.Errorf("expected rules ordered by id ascending, got ids %d, %d, %d", rules[0].ID, rules[1].ID, rules[2].ID)
	}
}

func TestRemoveRule_NotFound(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	err := svc.RemoveRule(999)
	if err == nil {
		t.Fatal("expected error for non-existent rule")
	}
}

//nolint:paralleltest,tparallel // subtests share parent's database
func TestApplyRules(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	// Seed an account
	millID := seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")

	// Seed one blank row that SHOULD match the rule
	_, err := dbase.Exec(
		`INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency,
		 creditor_iban, debtor_iban, transfer_title, category, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"BLANK-MATCH", millID, "2026-09-01", -5000, "PLN",
		"PL92116022020000000575810839", "", "JMP S.A. BIEDRONKA 3698  WROCLAW POL 2026-08-25", "", "[]",
	)
	if err != nil {
		t.Fatalf("seeding blank match: %v", err)
	}

	// Seed one row already categorised that SHOULD NOT be touched
	_, err = dbase.Exec(
		`INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency,
		 creditor_iban, debtor_iban, transfer_title, category, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"ALREADY-CAT", millID, "2026-09-01", -3000, "PLN",
		"PL92116022020000000575810839", "", "BIEDRONKA", "Groceries", `["manual"]`,
	)
	if err != nil {
		t.Fatalf("seeding already categorised: %v", err)
	}

	// Seed one blank row that does NOT match any rule
	_, err = dbase.Exec(
		`INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, currency,
		 creditor_iban, debtor_iban, transfer_title, category, tags)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"BLANK-NOMATCH", millID, "2026-09-02", -10000, "PLN",
		"PL000000000000000000000000", "", "Some random description", "", "[]",
	)
	if err != nil {
		t.Fatalf("seeding blank no match: %v", err)
	}

	// Add a rule that should match the first blank row
	_, err = svc.AddRule("title", "BIEDRONKA", "Groceries", `["auto"]`)
	if err != nil {
		t.Fatalf("add rule: %v", err)
	}

	t.Run("assigns blank rows only", func(t *testing.T) {
		result, err := svc.ApplyRules(false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Scanned != 2 {
			t.Errorf("expected scanned=2, got %d", result.Scanned)
		}
		if result.Matched != 1 {
			t.Errorf("expected matched=1, got %d", result.Matched)
		}
		if result.Updated != 1 {
			t.Errorf("expected updated=1, got %d", result.Updated)
		}
		if result.DryRun {
			t.Error("expected dry_run=false")
		}

		// Verify blank-match row now has the category
		var category, tags string
		err = dbase.QueryRow(
			"SELECT category, tags FROM transactions WHERE bank_transaction_id = 'BLANK-MATCH'",
		).Scan(&category, &tags)
		if err != nil {
			t.Fatalf("querying BLANK-MATCH: %v", err)
		}
		if category != "Groceries" {
			t.Errorf("expected Groceries, got %s", category)
		}
		if tags != `["auto"]` {
			t.Errorf("expected [\"auto\"], got %s", tags)
		}

		// Verify already-categorised row is untouched
		err = dbase.QueryRow(
			"SELECT category, tags FROM transactions WHERE bank_transaction_id = 'ALREADY-CAT'",
		).Scan(&category, &tags)
		if err != nil {
			t.Fatalf("querying ALREADY-CAT: %v", err)
		}
		if category != "Groceries" {
			t.Errorf("expected Groceries, got %s", category)
		}
		if tags != `["manual"]` {
			t.Errorf("expected [\"manual\"], got %s", tags)
		}

		// Verify non-matching blank row is still blank
		err = dbase.QueryRow(
			"SELECT category FROM transactions WHERE bank_transaction_id = 'BLANK-NOMATCH'",
		).Scan(&category)
		if err != nil {
			t.Fatalf("querying BLANK-NOMATCH: %v", err)
		}
		if category != "" {
			t.Errorf("expected empty category, got %s", category)
		}
	})

	t.Run("dry run changes nothing", func(t *testing.T) {
		// Snapshot DB state
		var countBefore int
		_ = dbase.QueryRow("SELECT COUNT(*) FROM transactions WHERE category = 'Groceries'").Scan(&countBefore)

		result, err := svc.ApplyRules(true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.DryRun {
			t.Error("expected dry_run=true")
		}
		if result.Updated != 0 {
			t.Errorf("expected updated=0 in dry run, got %d", result.Updated)
		}

		var countAfter int
		_ = dbase.QueryRow("SELECT COUNT(*) FROM transactions WHERE category = 'Groceries'").Scan(&countAfter)
		if countAfter != countBefore {
			t.Errorf("db changed during dry run: before=%d, after=%d", countBefore, countAfter)
		}
	})

	t.Run("idempotent second run", func(t *testing.T) {
		result, err := svc.ApplyRules(false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Updated != 0 {
			t.Errorf("expected updated=0 on second run, got %d", result.Updated)
		}
	})
}
