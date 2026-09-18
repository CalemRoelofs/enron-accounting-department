//nolint:govet // err shadowing in assertion helpers is intentional
package service_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/calemroelofs/enron-accounting-department/internal/service"
)

func writeOrderFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func copyOrderFixture(t *testing.T, dir, src, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", src))
	if err != nil {
		t.Fatalf("reading fixture %s: %v", src, err)
	}
	writeOrderFile(t, dir, name, data)
}

func seedOrderTransaction(t *testing.T, dbase *sql.DB, id int64, amountCents int64) {
	t.Helper()
	accountID := seedAccount(t, dbase, "Test", "PL111111111111111111111111")
	if _, err := dbase.Exec(
		`INSERT INTO transactions (id, bank_transaction_id, account_id, date, amount_cents)
		 VALUES (?, 'TX-ORDER-1', ?, '2026-09-09', ?)`,
		id, accountID, amountCents,
	); err != nil {
		t.Fatalf("seeding transaction %d: %v", id, err)
	}
}

func TestImportOrders_StoresRows(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	copyOrderFixture(t, dir, "orders.json", "orders.json")

	result, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("ImportOrders failed: %v", err)
	}
	if result.Imported != 2 {
		t.Errorf("expected 2 imported, got %d", result.Imported)
	}
	if result.Items != 3 {
		t.Errorf("expected 3 items, got %d", result.Items)
	}
	if result.Refunds != 1 {
		t.Errorf("expected 1 refund, got %d", result.Refunds)
	}
	if result.Skipped != 0 {
		t.Errorf("expected 0 skipped, got %d", result.Skipped)
	}
	if result.Unlinked != 2 {
		t.Errorf("expected 2 unlinked (no transactions seeded), got %d", result.Unlinked)
	}

	var seller, paymentMethod, currency string
	var paid, refunded, delivery int64
	if err := dbase.QueryRow(
		`SELECT paid_cents, refunded_cents, currency, seller, payment_method, delivery_cents
		 FROM orders WHERE source = 'allegro' AND order_ref = 'ORDER-A-0001'`,
	).Scan(&paid, &refunded, &currency, &seller, &paymentMethod, &delivery); err != nil {
		t.Fatalf("querying order A-0001: %v", err)
	}
	if paid != 5473 || refunded != 1900 || currency != "PLN" || delivery != 0 {
		t.Errorf("unexpected order A-0001: paid=%d refunded=%d currency=%q delivery=%d",
			paid, refunded, currency, delivery)
	}
	if seller != "seller-one" || paymentMethod != "Google Pay" {
		t.Errorf("unexpected order A-0001 metadata: seller=%q method=%q", seller, paymentMethod)
	}

	var refundedB int64
	if err := dbase.QueryRow(
		`SELECT refunded_cents FROM orders WHERE order_ref = 'ORDER-A-0002'`,
	).Scan(&refundedB); err != nil {
		t.Fatalf("querying order A-0002: %v", err)
	}
	if refundedB != 0 {
		t.Errorf("expected order A-0002 refunded 0, got %d", refundedB)
	}

	var itemRows, refundRows int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM order_items").Scan(&itemRows); err != nil {
		t.Fatalf("counting order_items: %v", err)
	}
	if err := dbase.QueryRow("SELECT COUNT(*) FROM refunds").Scan(&refundRows); err != nil {
		t.Fatalf("counting refunds: %v", err)
	}
	if itemRows != 3 {
		t.Errorf("expected 3 order_items rows, got %d", itemRows)
	}
	if refundRows != 1 {
		t.Errorf("expected 1 refunds row, got %d", refundRows)
	}

	var code *string
	var unitCents *int64
	if err := dbase.QueryRow(
		`SELECT code, unit_price_cents FROM order_items WHERE name = 'Battery'`,
	).Scan(&code, &unitCents); err != nil {
		t.Fatalf("querying nullable item: %v", err)
	}
	if code != nil || unitCents != nil {
		t.Errorf("expected NULL code/unit_price for Battery, got %v/%v", code, unitCents)
	}
}

func TestImportOrders_Idempotent(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	copyOrderFixture(t, dir, "orders.json", "orders.json")

	first, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("first import failed: %v", err)
	}
	if first.Imported != 2 {
		t.Errorf("expected 2 imported on first run, got %d", first.Imported)
	}

	second, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("second import failed: %v", err)
	}
	if second.Imported != 0 || second.Unchanged != 2 {
		t.Errorf("expected imported=0 unchanged=2 on re-run, got imported=%d unchanged=%d",
			second.Imported, second.Unchanged)
	}
	if second.Items != 0 || second.Refunds != 0 {
		t.Errorf("expected no new items/refunds on re-run, got items=%d refunds=%d",
			second.Items, second.Refunds)
	}

	var orders, items, refunds int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM orders").Scan(&orders); err != nil {
		t.Fatalf("counting orders: %v", err)
	}
	if err := dbase.QueryRow("SELECT COUNT(*) FROM order_items").Scan(&items); err != nil {
		t.Fatalf("counting order_items: %v", err)
	}
	if err := dbase.QueryRow("SELECT COUNT(*) FROM refunds").Scan(&refunds); err != nil {
		t.Fatalf("counting refunds: %v", err)
	}
	if orders != 2 || items != 3 || refunds != 1 {
		t.Errorf("expected 2 orders/3 items/1 refund after re-run, got %d/%d/%d", orders, items, refunds)
	}
}

func TestImportOrders_SkipsBadFiles(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	writeOrderFile(t, dir, "empty.json", nil)
	writeOrderFile(t, dir, "malformed.json", []byte(`{not valid json`))
	writeOrderFile(t, dir, "nonarray.json", []byte(`{"source":"x"}`))
	copyOrderFixture(t, dir, "orders_minimal.json", "good.json")

	result, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("ImportOrders failed: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("expected only good.json imported, got %d", result.Imported)
	}
	if result.Skipped != 3 {
		t.Errorf("expected 3 skipped, got %d", result.Skipped)
	}

	skipped := make(map[string]bool, len(result.SkippedFiles))
	for _, name := range result.SkippedFiles {
		skipped[name] = true
	}
	for _, want := range []string{"empty.json", "malformed.json", "nonarray.json"} {
		if !skipped[want] {
			t.Errorf("expected %s in skipped_files, got %v", want, result.SkippedFiles)
		}
	}
	if skipped["good.json"] {
		t.Error("good.json must not be skipped")
	}
}

func TestImportOrders_UnlinkedTransaction(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	copyOrderFixture(t, dir, "orders.json", "orders.json")

	result, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("ImportOrders failed: %v", err)
	}
	if result.Unlinked != 2 || result.Linked != 0 {
		t.Errorf("expected linked=0 unlinked=2, got linked=%d unlinked=%d", result.Linked, result.Unlinked)
	}

	var txID *int64
	if err := dbase.QueryRow(
		`SELECT transaction_id FROM orders WHERE order_ref = 'ORDER-A-0001'`,
	).Scan(&txID); err != nil {
		t.Fatalf("querying order A-0001: %v", err)
	}
	if txID != nil {
		t.Errorf("expected NULL transaction_id for missing bank transaction, got %d", *txID)
	}
}

func TestImportOrders_LinksExistingTransaction(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	seedOrderTransaction(t, dbase, 6772, -5473)

	dir := t.TempDir()
	copyOrderFixture(t, dir, "orders.json", "orders.json")

	result, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("ImportOrders failed: %v", err)
	}
	if result.Linked != 1 || result.Unlinked != 1 {
		t.Errorf("expected linked=1 unlinked=1, got linked=%d unlinked=%d", result.Linked, result.Unlinked)
	}

	var txID *int64
	if err := dbase.QueryRow(
		`SELECT transaction_id FROM orders WHERE order_ref = 'ORDER-A-0001'`,
	).Scan(&txID); err != nil {
		t.Fatalf("querying order A-0001: %v", err)
	}
	if txID == nil {
		t.Fatal("expected order A-0001 linked to transaction 6772, got NULL")
	}
	if *txID != 6772 {
		t.Errorf("expected transaction_id 6772, got %d", *txID)
	}
}

func TestImportOrders_AbsentRefundsAndOptionals(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	copyOrderFixture(t, dir, "orders_minimal.json", "minimal.json")

	result, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("ImportOrders failed: %v", err)
	}
	if result.Imported != 1 || result.Items != 1 || result.Refunds != 0 {
		t.Errorf("unexpected counts: imported=%d items=%d refunds=%d",
			result.Imported, result.Items, result.Refunds)
	}

	var refunded int64
	var seller, paymentMethod *string
	var currency string
	if err := dbase.QueryRow(
		`SELECT refunded_cents, seller, payment_method, currency FROM orders WHERE order_ref = 'MIN-0001'`,
	).Scan(&refunded, &seller, &paymentMethod, &currency); err != nil {
		t.Fatalf("querying minimal order: %v", err)
	}
	if refunded != 0 {
		t.Errorf("expected refunded_cents 0 for absent refunds, got %d", refunded)
	}
	if seller != nil || paymentMethod != nil {
		t.Errorf("expected NULL seller/payment_method, got %v/%v", seller, paymentMethod)
	}
	if currency != "PLN" {
		t.Errorf("expected default currency PLN, got %q", currency)
	}
}

func TestImportOrders_DeduplicatesAcrossFiles(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	copyOrderFixture(t, dir, "orders.json", "a.json")
	copyOrderFixture(t, dir, "orders.json", "b.json")

	result, err := svc.ImportOrders(dir)
	if err != nil {
		t.Fatalf("ImportOrders failed: %v", err)
	}
	if result.Imported != 2 || result.Unchanged != 2 {
		t.Errorf("expected imported=2 unchanged=2, got imported=%d unchanged=%d",
			result.Imported, result.Unchanged)
	}

	var orders int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM orders").Scan(&orders); err != nil {
		t.Fatalf("counting orders: %v", err)
	}
	if orders != 2 {
		t.Errorf("expected 2 orders after duplicate files, got %d", orders)
	}
}
