//nolint:govet // err shadowing in assertion helpers is intentional
package service_test

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/calemroelofs/enron-accounting-department/internal/service"
)

// receiptFixture describes a synthetic Biedronka receipt used to build wire-format files.
type receiptFixture struct {
	store       string
	nrDok       int
	purchasedAt string
	positions   []any
	sumaBrutto  int64
	sumaOpust   *int64
	deposit     int64
	paid        int64
}

func (f receiptFixture) document() map[string]any {
	podsum := map[string]any{
		"waluta":     "PLN",
		"sumaPod":    0,
		"sumaBrutto": f.sumaBrutto,
	}
	if f.sumaOpust != nil {
		podsum["sumaOpust"] = *f.sumaOpust
	}

	return map[string]any{
		"naglowek": map[string]any{"wersja": "JPK_KASA_PARAGON_v2-0", "dataJPK": f.purchasedAt},
		"podmiot1": map[string]any{
			"NIP":      "1234567890",
			"nazwaPod": "TEST STORE S.A.",
			"nrUnik":   f.store,
			"nrFabr":   "FAB0001",
		},
		"paragon": map[string]any{
			"JPKID":     1,
			"nrDok":     f.nrDok,
			"nrKasy":    "Kasa 1",
			"kasjer":    "Kasjer 1",
			"zakSprzed": f.purchasedAt,
			"nrParag":   1,
			"pozycja":   f.positions,
			"podsum":    podsum,
			"opak":      map[string]any{"daneOpak": []any{}, "wart": f.deposit},
			"total":     map[string]any{"zaplZwrot": f.paid},
			"platnosc": []any{map[string]any{
				"reszta": false, "forma": "2", "wart": f.paid, "nazwa": "Visa Debit 07 1",
			}},
		},
	}
}

func envelopeBytes(t *testing.T, document map[string]any) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"dokument": document})
	if err != nil {
		t.Fatalf("marshaling receipt payload: %v", err)
	}
	data := "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	raw, err := json.Marshal(map[string]any{
		"protoVersion": "000",
		"IDZ":          "c=test|g={test}|s=3698|p=9|t=4608",
		"deviceType":   2,
		"printed":      false,
		"data":         data,
		"header":       []any{},
		"sign":         "fake-signature",
	})
	if err != nil {
		t.Fatalf("marshaling receipt envelope: %v", err)
	}
	return raw
}

func receiptItemsFixture() []any {
	return []any{
		map[string]any{"towar": map[string]any{
			"nazwa":   "Łosoś  atl  fil",
			"ilosc":   "0,326",
			"cena":    7990,
			"brutto":  1953,
			"idStPTU": "C",
			"oper":    false,
			"rabat":   map[string]any{"opis": "1", "wart": -652},
		}},
		map[string]any{"discountLine": map[string]any{
			"base": 1953, "value": 652, "isDiscount": true, "isPercent": false,
		}},
		map[string]any{"towar": map[string]any{
			"nazwa":   "Mleko 2%",
			"ilosc":   "1",
			"cena":    350,
			"brutto":  350,
			"idStPTU": "A",
			"oper":    false,
		}},
	}
}

func sampleFixture() receiptFixture {
	discount := int64(-652)
	return receiptFixture{
		store:       "STORE-0001",
		nrDok:       1001,
		purchasedAt: "2026-08-28T10:30:45.000Z",
		positions:   receiptItemsFixture(),
		sumaBrutto:  2303,
		sumaOpust:   &discount,
		deposit:     50,
		paid:        2353,
	}
}

func writeReceiptFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func TestParseReceipt(t *testing.T) {
	t.Parallel()
	fix := sampleFixture()

	parsed, err := service.ParseReceipt(envelopeBytes(t, fix.document()))
	if err != nil {
		t.Fatalf("ParseReceipt failed: %v", err)
	}

	if parsed.StoreID != "STORE-0001" {
		t.Errorf("expected store STORE-0001, got %s", parsed.StoreID)
	}
	if parsed.NrDok != 1001 {
		t.Errorf("expected nr_dok 1001, got %d", parsed.NrDok)
	}
	if parsed.PurchasedAt != "2026-08-28T10:30:45.000Z" {
		t.Errorf("unexpected purchased_at %s", parsed.PurchasedAt)
	}
	if parsed.TotalCents != 2303 {
		t.Errorf("expected total 2303, got %d", parsed.TotalCents)
	}
	if parsed.DiscountCents != 652 {
		t.Errorf("expected discount 652, got %d", parsed.DiscountCents)
	}
	if parsed.DepositCents != 50 {
		t.Errorf("expected deposit 50, got %d", parsed.DepositCents)
	}
	if parsed.PaidCents != parsed.TotalCents+parsed.DepositCents {
		t.Errorf("paid %d != total %d + deposit %d", parsed.PaidCents, parsed.TotalCents, parsed.DepositCents)
	}
	if parsed.PaymentForm != "Visa Debit 07 1" {
		t.Errorf("unexpected payment form %q", parsed.PaymentForm)
	}

	if len(parsed.Items) != 2 {
		t.Fatalf("expected 2 items (discountLine ignored), got %d", len(parsed.Items))
	}
	first := parsed.Items[0]
	if first.Name != "Łosoś atl fil" {
		t.Errorf("expected collapsed name 'Łosoś atl fil', got %q", first.Name)
	}
	if first.Quantity != "0,326" {
		t.Errorf("expected quantity '0,326', got %q", first.Quantity)
	}
	if first.BruttoCents != 1953 {
		t.Errorf("expected item brutto 1953, got %d", first.BruttoCents)
	}
	if first.DiscountCents != 652 {
		t.Errorf("expected item discount 652, got %d", first.DiscountCents)
	}
	if first.VATClass != "C" {
		t.Errorf("expected vat class C, got %q", first.VATClass)
	}
	if parsed.Items[1].Name != "Mleko 2%" {
		t.Errorf("unexpected second item %q", parsed.Items[1].Name)
	}
	if parsed.Items[1].DiscountCents != 0 {
		t.Errorf("expected no discount on second item, got %d", parsed.Items[1].DiscountCents)
	}
}

func TestParseReceipt_NoDiscountOmitsSumaOpust(t *testing.T) {
	t.Parallel()
	fix := sampleFixture()
	fix.sumaOpust = nil
	fix.sumaBrutto = 350
	fix.deposit = 0
	fix.paid = 350
	fix.positions = []any{map[string]any{"towar": map[string]any{
		"nazwa": "Mleko 2%", "ilosc": "1", "cena": 350, "brutto": 350, "idStPTU": "A",
	}}}

	parsed, err := service.ParseReceipt(envelopeBytes(t, fix.document()))
	if err != nil {
		t.Fatalf("ParseReceipt failed: %v", err)
	}
	if parsed.DiscountCents != 0 {
		t.Errorf("expected discount 0 when sumaOpust absent, got %d", parsed.DiscountCents)
	}
}

func TestImportReceipts_StoresRows(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	writeReceiptFile(t, dir, "good.json", envelopeBytes(t, sampleFixture().document()))

	result, err := svc.ImportReceipts(dir)
	if err != nil {
		t.Fatalf("ImportReceipts failed: %v", err)
	}
	if result.Status != "success" {
		t.Errorf("expected success, got %s", result.Status)
	}
	if result.Imported != 1 {
		t.Errorf("expected 1 imported, got %d", result.Imported)
	}
	if result.Items != 2 {
		t.Errorf("expected 2 items, got %d", result.Items)
	}
	if result.Skipped != 0 {
		t.Errorf("expected 0 skipped, got %d", result.Skipped)
	}

	var store, purchasedAt, paymentForm, sourceFile string
	var total, paid, discount, deposit, itemCount int64
	err = dbase.QueryRow(
		`SELECT store_id, purchased_at, total_cents, paid_cents, discount_cents,
		        deposit_cents, payment_form, item_count, source_file
		 FROM receipts`,
	).Scan(&store, &purchasedAt, &total, &paid, &discount, &deposit, &paymentForm, &itemCount, &sourceFile)
	if err != nil {
		t.Fatalf("querying receipt: %v", err)
	}
	if store != "STORE-0001" || total != 2303 || paid != 2353 || discount != 652 || deposit != 50 {
		t.Errorf("unexpected receipt row: %s %d %d %d %d", store, total, paid, discount, deposit)
	}
	if paymentForm != "Visa Debit 07 1" || itemCount != 2 || sourceFile != "good.json" {
		t.Errorf("unexpected receipt metadata: %q %d %q", paymentForm, itemCount, sourceFile)
	}

	var itemRows int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM receipt_items").Scan(&itemRows); err != nil {
		t.Fatalf("counting items: %v", err)
	}
	if itemRows != 2 {
		t.Errorf("expected 2 receipt_items rows, got %d", itemRows)
	}
}

func TestImportReceipts_SkipsBadFiles(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	writeReceiptFile(t, dir, "empty.json", nil)
	writeReceiptFile(t, dir, "nodata.json", []byte(`{"protoVersion":"000","data":""}`))
	writeReceiptFile(t, dir, "invalid.json", []byte(`{not valid json`))
	writeReceiptFile(t, dir, "badb64.json", []byte(`{"data":"header.!!!notbase64!!!.sig"}`))
	writeReceiptFile(t, dir, "noparagon.json", envelopeBytes(t, map[string]any{
		"podmiot1": map[string]any{"nrUnik": "STORE-0002"},
	}))
	writeReceiptFile(t, dir, "good.json", envelopeBytes(t, sampleFixture().document()))

	result, err := svc.ImportReceipts(dir)
	if err != nil {
		t.Fatalf("ImportReceipts failed: %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("expected only good.json imported, got %d", result.Imported)
	}
	if result.Skipped != 5 {
		t.Errorf("expected 5 skipped, got %d", result.Skipped)
	}

	skipped := make(map[string]bool, len(result.SkippedFiles))
	for _, name := range result.SkippedFiles {
		skipped[name] = true
	}
	for _, want := range []string{"empty.json", "nodata.json", "invalid.json", "badb64.json", "noparagon.json"} {
		if !skipped[want] {
			t.Errorf("expected %s in skipped_files, got %v", want, result.SkippedFiles)
		}
	}
	if skipped["good.json"] {
		t.Error("good.json must not be skipped")
	}
}

func TestImportReceipts_Idempotent(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	dir := t.TempDir()
	fix := sampleFixture()
	writeReceiptFile(t, dir, "a.json", envelopeBytes(t, fix.document()))
	writeReceiptFile(t, dir, "b.json", envelopeBytes(t, fix.document()))

	first, err := svc.ImportReceipts(dir)
	if err != nil {
		t.Fatalf("first import failed: %v", err)
	}
	if first.Imported != 1 {
		t.Errorf("expected exactly one receipt from duplicate files, got %d", first.Imported)
	}

	second, err := svc.ImportReceipts(dir)
	if err != nil {
		t.Fatalf("second import failed: %v", err)
	}
	if second.Imported != 0 || second.Items != 0 {
		t.Errorf("expected no changes on re-run, got imported=%d items=%d", second.Imported, second.Items)
	}

	var receipts, items int
	if err := dbase.QueryRow("SELECT COUNT(*) FROM receipts").Scan(&receipts); err != nil {
		t.Fatalf("counting receipts: %v", err)
	}
	if err := dbase.QueryRow("SELECT COUNT(*) FROM receipt_items").Scan(&items); err != nil {
		t.Fatalf("counting items: %v", err)
	}
	if receipts != 1 {
		t.Errorf("expected 1 receipt after two runs, got %d", receipts)
	}
	if items != 2 {
		t.Errorf("expected 2 items after two runs, got %d", items)
	}
}

func TestImportReceipts_LinksTransactions(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	accountID := seedAccount(t, dbase, "Millennium", "PL987654321098765432109876")

	txA := seedTransaction(t, dbase, accountID, "A", "JMP BIEDRONKA 3698 WROCLAW POL 2026-08-28", -2353)
	seedTransaction(t, dbase, accountID, "B", "JMP BIEDRONKA 3698 WROCLAW POL 2026-08-30", -500)
	seedTransaction(t, dbase, accountID, "C", "JMP BIEDRONKA 3698 WROCLAW POL 2026-08-31", -700)

	dir := t.TempDir()
	discount := int64(-652)
	writeReceiptFile(t, dir, "linked.json", envelopeBytes(t, receiptFixture{
		store: "STORE-A", nrDok: 1, purchasedAt: "2026-08-28T10:30:45.000Z",
		positions: receiptItemsFixture(), sumaBrutto: 2303, sumaOpust: &discount, deposit: 50, paid: 2353,
	}.document()))
	writeReceiptFile(t, dir, "wrongdate.json", envelopeBytes(t, receiptFixture{
		store: "STORE-B", nrDok: 2, purchasedAt: "2026-08-29T09:00:00.000Z",
		positions:  []any{map[string]any{"towar": map[string]any{"nazwa": "Chleb", "brutto": 500}}},
		sumaBrutto: 500, deposit: 0, paid: 500,
	}.document()))
	writeReceiptFile(t, dir, "wrongamount.json", envelopeBytes(t, receiptFixture{
		store: "STORE-C", nrDok: 3, purchasedAt: "2026-08-31T18:00:00.000Z",
		positions:  []any{map[string]any{"towar": map[string]any{"nazwa": "Masło", "brutto": 600}}},
		sumaBrutto: 600, deposit: 0, paid: 600,
	}.document()))

	result, err := svc.ImportReceipts(dir)
	if err != nil {
		t.Fatalf("ImportReceipts failed: %v", err)
	}
	if result.Linked != 1 {
		t.Errorf("expected 1 linked receipt, got %d", result.Linked)
	}
	if result.Unlinked != 2 {
		t.Errorf("expected 2 unlinked receipts, got %d", result.Unlinked)
	}

	assertReceiptLink(t, dbase, "STORE-A", txA)
	assertReceiptLink(t, dbase, "STORE-B", 0)
	assertReceiptLink(t, dbase, "STORE-C", 0)

	// The transactions table must never be mutated by the importer.
	var amountA int64
	var titleA string
	if err := dbase.QueryRow(
		"SELECT amount_cents, transfer_title FROM transactions WHERE id = ?", txA,
	).Scan(&amountA, &titleA); err != nil {
		t.Fatalf("querying transaction A: %v", err)
	}
	if amountA != -2353 || titleA != "JMP BIEDRONKA 3698 WROCLAW POL 2026-08-28" {
		t.Errorf("transaction A was modified: amount=%d title=%q", amountA, titleA)
	}
}

func TestImportReceipts_EmptyOrMissingDir(t *testing.T) {
	t.Parallel()
	dbase := newTestDB(t)
	defer dbase.Close()
	svc := service.NewService(dbase)

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	result, err := svc.ImportReceipts(missing)
	if err != nil {
		t.Fatalf("missing dir should not error, got: %v", err)
	}
	if result.Imported != 0 || result.Skipped != 0 {
		t.Errorf("expected empty result for missing dir, got %+v", result)
	}

	empty := t.TempDir()
	result, err = svc.ImportReceipts(empty)
	if err != nil {
		t.Fatalf("empty dir should not error, got: %v", err)
	}
	if result.Imported != 0 || len(result.SkippedFiles) != 0 {
		t.Errorf("expected empty result for empty dir, got %+v", result)
	}
}

func seedTransaction(
	t *testing.T,
	dbase *sql.DB,
	accountID int64,
	ref, title string,
	amountCents int64,
) int64 {
	t.Helper()
	res, err := dbase.Exec(
		`INSERT INTO transactions (bank_transaction_id, account_id, date, amount_cents, transfer_title)
		 VALUES (?, ?, '2026-09-01', ?, ?)`,
		ref, accountID, amountCents, title,
	)
	if err != nil {
		t.Fatalf("seeding transaction %s: %v", ref, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("reading transaction id: %v", err)
	}
	return id
}

func assertReceiptLink(t *testing.T, dbase *sql.DB, store string, want int64) {
	t.Helper()
	var got *int64
	if err := dbase.QueryRow(
		"SELECT transaction_id FROM receipts WHERE store_id = ?", store,
	).Scan(&got); err != nil {
		t.Fatalf("querying receipt %s: %v", store, err)
	}
	if want == 0 {
		if got != nil {
			t.Errorf("expected receipt %s unlinked, got transaction_id %d", store, *got)
		}
		return
	}
	if got == nil {
		t.Errorf("expected receipt %s linked to %d, got NULL", store, want)
		return
	}
	if *got != want {
		t.Errorf("expected receipt %s linked to %d, got %d", store, want, *got)
	}
}
