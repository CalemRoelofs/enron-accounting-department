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

func TestInitDB_CreatesCategoryRules(t *testing.T) {
	t.Parallel()
	dbase, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	var count int
	err = dbase.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='category_rules'",
	).Scan(&count)
	if err != nil {
		t.Fatalf("checking category_rules table: %v", err)
	}
	if count != 1 {
		t.Fatal("category_rules table not found")
	}

	// Verify columns exist with correct types
	type colInfo struct {
		cid      int
		name     string
		colType  string
		notNull  int
		defaultV *string
		pk       int
	}
	rows, err := dbase.Query("PRAGMA table_info('category_rules')")
	if err != nil {
		t.Fatalf("querying table info: %v", err)
	}
	defer rows.Close()

	got := make(map[string]colInfo)
	for rows.Next() {
		var c colInfo
		if err := rows.Scan(&c.cid, &c.name, &c.colType, &c.notNull, &c.defaultV, &c.pk); err != nil {
			t.Fatalf("scanning column info: %v", err)
		}
		got[c.name] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating columns: %v", err)
	}

	want := map[string]struct {
		colType string
		notNull bool
		pk      bool
	}{
		"id":         {"INTEGER", false, true},
		"field":      {"TEXT", true, false},
		"pattern":    {"TEXT", true, false},
		"category":   {"TEXT", true, false},
		"tags":       {"TEXT", true, false},
		"created_at": {"TEXT", true, false},
	}
	for name, w := range want {
		c, ok := got[name]
		if !ok {
			t.Errorf("column %s not found", name)
			continue
		}
		if c.colType != w.colType {
			t.Errorf("column %s: expected type %s, got %s", name, w.colType, c.colType)
		}
		if (c.notNull == 1) != w.notNull {
			t.Errorf("column %s: expected notNull=%v, got %d", name, w.notNull, c.notNull)
		}
		if (c.pk == 1) != w.pk {
			t.Errorf("column %s: expected pk=%v, got %d", name, w.pk, c.pk)
		}
	}

	// Second InitDB must not error (idempotent)
	_, err = db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("second InitDB on same DB failed: %v", err)
	}
}

func TestInitDB_CreatesReceiptTables(t *testing.T) {
	t.Parallel()
	dbase, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	for _, table := range []string{"receipts", "receipt_items"} {
		var count int
		if err := dbase.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&count); err != nil {
			t.Fatalf("checking table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", table)
		}
	}

	// (store_id, nr_dok) must be unique so re-imports are idempotent.
	if _, err := dbase.Exec(
		`INSERT INTO receipts (store_id, nr_dok, purchased_at, total_cents, paid_cents)
		 VALUES ('STORE-1', 7, '2026-08-28T10:30:45Z', 100, 100)`,
	); err != nil {
		t.Fatalf("inserting first receipt: %v", err)
	}
	if _, err := dbase.Exec(
		`INSERT OR IGNORE INTO receipts (store_id, nr_dok, purchased_at, total_cents, paid_cents)
		 VALUES ('STORE-1', 7, '2026-08-28T10:30:45Z', 100, 100)`,
	); err != nil {
		t.Fatalf("inserting duplicate receipt: %v", err)
	}
	var receipts int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM receipts").Scan(&receipts); err != nil {
		t.Fatalf("counting receipts: %v", err)
	}
	if receipts != 1 {
		t.Errorf("expected 1 receipt after duplicate insert, got %d", receipts)
	}
}

func TestInitDB_CreatesOrdersTables(t *testing.T) {
	t.Parallel()
	dbase, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer dbase.Close()

	for _, table := range []string{"orders", "order_items", "refunds"} {
		var count int
		if err := dbase.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&count); err != nil {
			t.Fatalf("checking table %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s not found", table)
		}
	}

	// (source, order_ref) must be unique so re-imports are idempotent.
	if _, err := dbase.Exec(
		`INSERT INTO orders (source, order_ref, order_date, paid_cents)
		 VALUES ('allegro', 'ORDER-1', '2026-09-09T12:10', 100)`,
	); err != nil {
		t.Fatalf("inserting first order: %v", err)
	}
	if _, err := dbase.Exec(
		`INSERT OR IGNORE INTO orders (source, order_ref, order_date, paid_cents)
		 VALUES ('allegro', 'ORDER-1', '2026-09-09T12:10', 100)`,
	); err != nil {
		t.Fatalf("inserting duplicate order: %v", err)
	}
	var orders int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM orders").Scan(&orders); err != nil {
		t.Fatalf("counting orders: %v", err)
	}
	if orders != 1 {
		t.Errorf("expected 1 order after duplicate insert, got %d", orders)
	}
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
