// Package db provides database initialization and schema management.
//
//nolint:noctx // deliberate: DB operations use background context implicitly
package db

import (
	"database/sql"
	"fmt"

	// Register the sqlite driver.
	_ "modernc.org/sqlite"
)

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
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("creating schema: %w", err)
	}

	// Add new columns if missing (for existing databases).
	_, _ = db.Exec("ALTER TABLE bank_connections ADD COLUMN session_id TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE bank_connections ADD COLUMN aspsp_id TEXT NOT NULL DEFAULT ''")
	_, _ = db.Exec("ALTER TABLE accounts ADD COLUMN external_id TEXT NOT NULL DEFAULT ''")

	_, err := db.Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_account_tx ON transactions(bank_transaction_id, account_id)",
	)
	if err != nil {
		return err
	}

	return nil
}
