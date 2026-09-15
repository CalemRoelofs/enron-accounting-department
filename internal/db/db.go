// Package db provides database initialization and schema management.
//
//nolint:noctx // deliberate: DB operations use background context implicitly
package db

import (
	"database/sql"
	"fmt"
	"time"

	// Register the sqlite driver.
	_ "modernc.org/sqlite"
)

// payPeriodNormalizationVersion is the migration that repairs corrupt
// pay_periods rows (duplicates and end dates before start dates).
const payPeriodNormalizationVersion = 1

// receiptsSchemaVersion is the migration that adds the Biedronka e-receipt
// tables used for product-level spend analysis.
const receiptsSchemaVersion = 2

// InitDB opens or creates the database and applies the schema.
func InitDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening db: %w", err)
	}

	if _, jErr := db.Exec("PRAGMA journal_mode=WAL"); jErr != nil {
		return nil, fmt.Errorf("enabling WAL: %w", jErr)
	}

	if sErr := createSchema(db); sErr != nil {
		return nil, sErr
	}

	return db, nil
}

// InitDBReadOnly opens the database in read-only mode.
func InitDBReadOnly(path string) (*sql.DB, error) {
	uri := fmt.Sprintf("file:%s?mode=ro", path)
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return nil, fmt.Errorf("opening db read-only: %w", err)
	}
	return db, nil
}

func createSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		iban TEXT NOT NULL UNIQUE,
		type TEXT NOT NULL DEFAULT '',
		virtual_balance INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS transactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		bank_transaction_id TEXT NOT NULL,
		account_id INTEGER NOT NULL REFERENCES accounts(id),
		date TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		currency TEXT NOT NULL DEFAULT 'PLN',
		merchant_name TEXT NOT NULL DEFAULT '',
		transfer_title TEXT,
		creditor_iban TEXT NOT NULL DEFAULT '',
		debtor_iban TEXT NOT NULL DEFAULT '',
		credit_debit_indicator TEXT NOT NULL DEFAULT '',
		category TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '[]',
		notes TEXT,
		needs_review INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS line_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		transaction_id INTEGER NOT NULL REFERENCES transactions(id),
		item_name TEXT NOT NULL,
		amount_cents INTEGER NOT NULL,
		category TEXT NOT NULL DEFAULT '',
		tags TEXT NOT NULL DEFAULT '[]',
		notes TEXT
	);

	CREATE TABLE IF NOT EXISTS pay_periods (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		start_date TEXT NOT NULL,
		end_date TEXT
	);

	CREATE TABLE IF NOT EXISTS bank_connections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		provider TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		aspsp_id TEXT NOT NULL DEFAULT '',
		consent_granted_at TEXT NOT NULL,
		consent_expires_at TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS category_rules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		field TEXT NOT NULL,
		pattern TEXT NOT NULL,
		category TEXT NOT NULL,
		tags TEXT NOT NULL DEFAULT '[]',
		created_at TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}

	// Add new columns if missing (for existing databases).
	_, _ = db.Exec("ALTER TABLE bank_connections ADD COLUMN session_id TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE bank_connections ADD COLUMN aspsp_id TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE accounts ADD COLUMN external_id TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE transactions ADD COLUMN credit_debit_indicator TEXT NOT NULL DEFAULT ''")

	_, err := db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_account_tx ON transactions(bank_transaction_id, account_id)",
	)
	if err != nil {
		return err
	}

	if migrateErr := applyMigrations(db); migrateErr != nil {
		return migrateErr
	}

	return nil
}

// applyMigrations runs one-time schema migrations. Each migration is recorded
// in schema_migrations so it is skipped on subsequent opens.
func applyMigrations(db *sql.DB) error {
	payPeriodsDone, err := migrationApplied(db, payPeriodNormalizationVersion)
	if err != nil {
		return err
	}
	if !payPeriodsDone {
		if normalizeErr := normalizePayPeriods(db); normalizeErr != nil {
			return normalizeErr
		}
		if recordErr := recordMigration(db, payPeriodNormalizationVersion); recordErr != nil {
			return recordErr
		}
	}

	receiptsDone, err := migrationApplied(db, receiptsSchemaVersion)
	if err != nil {
		return err
	}
	if !receiptsDone {
		if schemaErr := createReceiptsSchema(db); schemaErr != nil {
			return schemaErr
		}
		if recordErr := recordMigration(db, receiptsSchemaVersion); recordErr != nil {
			return recordErr
		}
	}

	return nil
}

// migrationApplied reports whether the given schema version has already run.
func migrationApplied(db *sql.DB, version int) (bool, error) {
	var applied int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM schema_migrations WHERE version = ?",
		version,
	).Scan(&applied); err != nil {
		return false, fmt.Errorf("checking schema version: %w", err)
	}
	return applied > 0, nil
}

// recordMigration marks a schema version as applied.
func recordMigration(db *sql.DB, version int) error {
	if _, err := db.Exec(
		"INSERT OR IGNORE INTO schema_migrations (version) VALUES (?)",
		version,
	); err != nil {
		return fmt.Errorf("recording schema version: %w", err)
	}
	return nil
}

// createReceiptsSchema adds the tables that hold imported Biedronka
// e-receipts and their product line items.
func createReceiptsSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS receipts (
		id INTEGER PRIMARY KEY,
		store_id TEXT NOT NULL,
		nr_dok INTEGER NOT NULL,
		nr_fabr TEXT,
		purchased_at TEXT NOT NULL,
		total_cents INTEGER NOT NULL,
		paid_cents INTEGER NOT NULL,
		discount_cents INTEGER NOT NULL DEFAULT 0,
		deposit_cents INTEGER NOT NULL DEFAULT 0,
		payment_form TEXT,
		item_count INTEGER NOT NULL DEFAULT 0,
		source_file TEXT,
		transaction_id INTEGER REFERENCES transactions(id),
		UNIQUE(store_id, nr_dok)
	);

	CREATE TABLE IF NOT EXISTS receipt_items (
		id INTEGER PRIMARY KEY,
		receipt_id INTEGER NOT NULL REFERENCES receipts(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		quantity TEXT,
		unit_price_cents INTEGER,
		brutto_cents INTEGER NOT NULL,
		vat_class TEXT,
		discount_cents INTEGER NOT NULL DEFAULT 0,
		UNIQUE(receipt_id, name, quantity, brutto_cents)
	);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("creating receipts schema: %w", err)
	}
	return nil
}

// normalizePayPeriods repairs corrupt pay_periods rows. It is idempotent and
// leaves already-correct rows untouched: duplicate start_dates are collapsed
// (keeping the highest id) and every end_date is recomputed as the day before
// the next period's start_date, with the latest period left open.
func normalizePayPeriods(db *sql.DB) error {
	if _, err := db.Exec(
		`DELETE FROM pay_periods
		 WHERE id NOT IN (SELECT MAX(id) FROM pay_periods GROUP BY start_date)`,
	); err != nil {
		return fmt.Errorf("deduplicating pay periods: %w", err)
	}

	rows, err := db.Query("SELECT id, start_date, end_date FROM pay_periods ORDER BY start_date, id")
	if err != nil {
		return fmt.Errorf("querying pay periods: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type period struct {
		id        int64
		startDate string
		endDate   *string
	}

	var periods []period
	for rows.Next() {
		var p period
		if scanErr := rows.Scan(&p.id, &p.startDate, &p.endDate); scanErr != nil {
			return fmt.Errorf("scanning pay period: %w", scanErr)
		}
		periods = append(periods, p)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return fmt.Errorf("iterating pay periods: %w", rowsErr)
	}

	for i, p := range periods {
		var want *string
		if i < len(periods)-1 {
			next, parseErr := time.Parse("2006-01-02", periods[i+1].startDate)
			if parseErr != nil {
				return fmt.Errorf("parsing start date %q: %w", periods[i+1].startDate, parseErr)
			}
			closeDate := next.AddDate(0, 0, -1).Format("2006-01-02")
			want = &closeDate
		}

		if sameEndDate(p.endDate, want) {
			continue
		}

		if _, updateErr := db.Exec("UPDATE pay_periods SET end_date = ? WHERE id = ?", want, p.id); updateErr != nil {
			return fmt.Errorf("updating pay period %d: %w", p.id, updateErr)
		}
	}

	return nil
}

func sameEndDate(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
