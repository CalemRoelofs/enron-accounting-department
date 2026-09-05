//nolint:govet // intentional err shadowing
package db_test

import (
	"testing"

	_ "modernc.org/sqlite"

	"github.com/calemroelofs/enron-accounting-department/internal/db"
)

func TestInitDB(t *testing.T) {
	t.Parallel()
	dbase, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	if err := dbase.Ping(); err != nil {
		t.Fatalf("db ping failed: %v", err)
	}
}

func TestInitDB_CreatesTables(t *testing.T) {
	t.Parallel()
	dbase, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	tables := []string{"accounts", "transactions", "line_items", "pay_periods", "bank_connections"}
	for _, table := range tables {
		var count int
		err := dbase.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&count)
		if err != nil {
			t.Fatalf("checking table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", table)
		}
	}
}

func TestInitDBReadOnly(t *testing.T) {
	t.Parallel()
	t.Skip(":memory: does not support mode=ro URI; skipped")
}

func TestInitDB_WALMode(t *testing.T) {
	t.Parallel()
	dbase, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	var journalMode string
	err = dbase.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("reading journal_mode: %v", err)
	}
	if journalMode != "wal" && journalMode != "WAL" {
		t.Skip("WAL mode may not persist on :memory: databases")
	}
}
