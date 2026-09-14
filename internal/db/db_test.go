//nolint:govet // intentional err shadowing
package db_test

import (
	"database/sql"
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

func seedCorruptPayPeriods(t *testing.T, raw *sql.DB) {
	t.Helper()
	if _, err := raw.Exec(
		"CREATE TABLE pay_periods (id INTEGER PRIMARY KEY AUTOINCREMENT, start_date TEXT NOT NULL, end_date TEXT)",
	); err != nil {
		t.Fatalf("creating pay_periods: %v", err)
	}

	corrupt := []struct {
		id    int
		start string
		end   *string
	}{
		{13, "2026-06-29", new("2026-07-29")},
		{14, "2026-07-30", new("2026-08-26")},
		{15, "2026-08-27", new("2026-06-28")},
		{16, "2026-06-29", new("2026-07-29")},
		{17, "2026-07-30", new("2026-06-28")},
		{18, "2026-08-27", new("2026-08-26")},
		{19, "2026-06-29", new("2026-07-29")},
		{20, "2026-07-30", new("2026-08-26")},
		{21, "2026-08-27", nil},
	}
	for _, r := range corrupt {
		if _, err := raw.Exec(
			"INSERT INTO pay_periods (id, start_date, end_date) VALUES (?, ?, ?)",
			r.id, r.start, r.end,
		); err != nil {
			t.Fatalf("seeding period %d: %v", r.id, err)
		}
	}
}

func TestInitDB_NormalizesCorruptPayPeriods(t *testing.T) {
	t.Parallel()
	uri := "file:normalize_pay_periods_test?mode=memory&cache=shared"

	raw, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	defer raw.Close()

	seedCorruptPayPeriods(t, raw)

	dbase, err := db.InitDB(uri)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	type period struct {
		id    int64
		start string
		end   *string
	}
	rows, err := dbase.Query("SELECT id, start_date, end_date FROM pay_periods ORDER BY start_date")
	if err != nil {
		t.Fatalf("querying periods: %v", err)
	}
	defer rows.Close()

	var got []period
	for rows.Next() {
		var p period
		if err := rows.Scan(&p.id, &p.start, &p.end); err != nil {
			t.Fatalf("scanning period: %v", err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating periods: %v", err)
	}

	want := []period{
		{id: 19, start: "2026-06-29", end: new("2026-07-29")},
		{id: 20, start: "2026-07-30", end: new("2026-08-26")},
		{id: 21, start: "2026-08-27", end: nil},
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d periods after normalization, got %d", len(want), len(got))
	}
	for i, w := range want {
		if got[i].id != w.id {
			t.Errorf("period %d: expected id %d, got %d", i, w.id, got[i].id)
		}
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

func TestInitDB_NormalizationLeavesCorrectDataUntouched(t *testing.T) {
	t.Parallel()
	uri := "file:normalize_untouched_test?mode=memory&cache=shared"

	raw, err := sql.Open("sqlite", uri)
	if err != nil {
		t.Fatalf("opening raw db: %v", err)
	}
	defer raw.Close()

	if _, err := raw.Exec(
		"CREATE TABLE pay_periods (id INTEGER PRIMARY KEY AUTOINCREMENT, start_date TEXT NOT NULL, end_date TEXT)",
	); err != nil {
		t.Fatalf("creating pay_periods: %v", err)
	}
	correct := []struct {
		id    int
		start string
		end   *string
	}{
		{19, "2026-06-29", new("2026-07-29")},
		{20, "2026-07-30", new("2026-08-26")},
		{21, "2026-08-27", nil},
	}
	for _, r := range correct {
		if _, err := raw.Exec(
			"INSERT INTO pay_periods (id, start_date, end_date) VALUES (?, ?, ?)",
			r.id, r.start, r.end,
		); err != nil {
			t.Fatalf("seeding period %d: %v", r.id, err)
		}
	}

	dbase, err := db.InitDB(uri)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	var count int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM pay_periods").Scan(&count); err != nil {
		t.Fatalf("counting periods: %v", err)
	}
	if count != len(correct) {
		t.Fatalf("expected %d periods, got %d", len(correct), count)
	}

	for _, w := range correct {
		var start string
		var end *string
		if err := dbase.QueryRow(
			"SELECT start_date, end_date FROM pay_periods WHERE id = ?", w.id,
		).Scan(&start, &end); err != nil {
			t.Fatalf("querying period %d: %v", w.id, err)
		}
		if start != w.start {
			t.Errorf("period %d: expected start %s, got %s", w.id, w.start, start)
		}
		switch {
		case end == nil && w.end == nil:
		case end == nil || w.end == nil:
			t.Errorf("period %d: expected end %v, got %v", w.id, w.end, end)
		case *end != *w.end:
			t.Errorf("period %d: expected end %s, got %s", w.id, *w.end, *end)
		}
	}
}
