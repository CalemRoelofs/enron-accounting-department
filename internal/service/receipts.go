//nolint:govet,mnd // deeply nested JPK parsing shadows err/ok and uses fixed-format offsets
package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// errDuplicateReceipt signals that an identical receipt was already stored.
var errDuplicateReceipt = errors.New("duplicate receipt")

// Receipt represents a parsed Biedronka e-receipt.
type Receipt struct {
	StoreID       string        `json:"store_id"`
	NrDok         int           `json:"nr_dok"`
	PurchasedAt   string        `json:"purchased_at"`
	TotalCents    int64         `json:"total_cents"`
	DiscountCents int64         `json:"discount_cents"`
	DepositCents  int64         `json:"deposit_cents"`
	PaidCents     int64         `json:"paid_cents"`
	PaymentForm   string        `json:"payment_form"`
	Items         []ReceiptItem `json:"items"`
}

// ReceiptItem represents a single product line on a receipt.
type ReceiptItem struct {
	Name           string `json:"name"`
	Quantity       string `json:"quantity"`
	UnitPriceCents int64  `json:"unit_price_cents"`
	BruttoCents    int64  `json:"brutto_cents"`
	DiscountCents  int64  `json:"discount_cents"`
	VATClass       string `json:"vat_class"`
}

// ImportResult is the result of an import-receipts run.
type ImportResult struct {
	Status       string   `json:"status"`
	Imported     int      `json:"imported"`
	Items        int      `json:"items"`
	Unchanged    int      `json:"unchanged"`
	Skipped      int      `json:"skipped"`
	SkippedFiles []string `json:"skipped_files"`
	Linked       int      `json:"linked"`
	Unlinked     int      `json:"unlinked"`
}

var titleDateRe = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})$`)

// ParseReceipt parses a Biedronka e-receipt from its wire-format bytes.
func ParseReceipt(data []byte) (*Receipt, error) {
	var envelope struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshaling envelope: %w", err)
	}
	if envelope.Data == "" {
		return nil, fmt.Errorf("empty data field")
	}

	parts := strings.SplitN(envelope.Data, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWS format")
	}

	payload := parts[1]
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, err := base64.URLEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("base64 decoding: %w", err)
	}

	var doc struct {
		Dokument map[string]any `json:"dokument"`
	}
	if err := json.Unmarshal(decoded, &doc); err != nil {
		return nil, fmt.Errorf("unmarshaling document: %w", err)
	}
	if doc.Dokument == nil {
		return nil, fmt.Errorf("no dokument field")
	}

	return parseJPK(doc.Dokument)
}

//nolint:gocognit // parsing a deeply nested document requires multiple branches
func parseJPK(doc map[string]any) (*Receipt, error) {
	naglowek, _ := doc["naglowek"].(map[string]any)
	podmiot1, _ := doc["podmiot1"].(map[string]any)
	paragon, _ := doc["paragon"].(map[string]any)
	if paragon == nil {
		return nil, fmt.Errorf("no paragon in document")
	}

	r := &Receipt{}

	if podmiot1 != nil {
		r.StoreID, _ = stringField(podmiot1, "nrUnik")
	}
	r.NrDok = intField(paragon, "nrDok")
	r.PurchasedAt, _ = stringField(paragon, "zakSprzed")
	if r.PurchasedAt == "" && naglowek != nil {
		r.PurchasedAt, _ = stringField(naglowek, "dataJPK")
	}

	podsum, _ := paragon["podsum"].(map[string]any)
	if podsum != nil {
		r.TotalCents = int64Field(podsum, "sumaBrutto")
		if opust, ok := podsum["sumaOpust"]; ok {
			r.DiscountCents = absValue(opust)
		}
	}

	opak, _ := paragon["opak"].(map[string]any)
	if opak != nil {
		r.DepositCents = int64Field(opak, "wart")
	}

	total, _ := paragon["total"].(map[string]any)
	if total != nil {
		r.PaidCents = int64Field(total, "zaplZwrot")
	}

	if platnosc, ok := paragon["platnosc"].([]any); ok && len(platnosc) > 0 {
		if first, ok := platnosc[0].(map[string]any); ok {
			r.PaymentForm, _ = stringField(first, "nazwa")
		}
	}

	if pozycje, ok := paragon["pozycja"].([]any); ok {
		for _, p := range pozycje {
			entry, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if towar, ok := entry["towar"].(map[string]any); ok {
				r.Items = append(r.Items, parseTowar(towar))
			}
		}
	}

	return r, nil
}

func parseTowar(towar map[string]any) ReceiptItem {
	item := ReceiptItem{
		Name:           collapseSpace(stringFieldOr(towar, "nazwa")),
		Quantity:       stringFieldOr(towar, "ilosc"),
		UnitPriceCents: int64Field(towar, "cena"),
		BruttoCents:    int64Field(towar, "brutto"),
		VATClass:       stringFieldOr(towar, "idStPTU"),
	}
	if rabat, ok := towar["rabat"].(map[string]any); ok {
		item.DiscountCents = absValue(rabat["wart"])
	}
	return item
}

//nolint:unparam // bool is part of the two-value field lookup API
func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func stringFieldOr(m map[string]any, key string) string {
	s, _ := stringField(m, key)
	return s
}

func intField(m map[string]any, key string) int {
	return int(int64Field(m, key))
}

func int64Field(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func absValue(v any) int64 {
	switch n := v.(type) {
	case float64:
		if n < 0 {
			return int64(-n)
		}
		return int64(n)
	case int64:
		if n < 0 {
			return -n
		}
		return n
	case int:
		if n < 0 {
			return int64(-n)
		}
		return int64(n)
	}
	return 0
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ImportReceipts reads all files in dir, parses them as Biedronka e-receipts,
// stores valid ones in the database, and links them to matching transactions.
//
//nolint:noctx,gocognit,nilerr,funlen // DB uses background context; missing dir yields an empty result
func (s *Service) ImportReceipts(dir string) (*ImportResult, error) {
	result := &ImportResult{Status: statusSuccess}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return result, nil
	}

	// Pre-load all transactions once so we don't cross connections
	type txRow struct {
		id     int64
		amount int64
		title  string
	}
	var txs []txRow
	rows, err := s.DB.Query(
		`SELECT id, amount_cents, transfer_title FROM transactions
		 WHERE transfer_title IS NOT NULL`,
	)
	if err == nil {
		for rows.Next() {
			var r txRow
			if err := rows.Scan(&r.id, &r.amount, &r.title); err != nil {
				break
			}
			txs = append(txs, r)
		}
		//nolint:sqlclosecheck // closed before the import loop to avoid crossing connections
		_ = rows.Close()
		if rowsErr := rows.Err(); rowsErr != nil {
			result.Status = "partial"
		}
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)

		data, readErr := os.ReadFile(path)
		if readErr != nil || len(data) == 0 {
			result.Skipped++
			result.SkippedFiles = append(result.SkippedFiles, name)
			continue
		}

		parsed, parseErr := ParseReceipt(data)
		if parseErr != nil {
			result.Skipped++
			result.SkippedFiles = append(result.SkippedFiles, name)
			continue
		}

		var txID *int64
		purchaseDate := parsed.PurchasedAt
		if len(purchaseDate) >= 10 {
			purchaseDate = purchaseDate[:10]
		}
		for _, t := range txs {
			m := titleDateRe.FindString(t.title)
			if m != "" && m == purchaseDate && absInt64(t.amount) == parsed.PaidCents {
				v := t.id
				txID = &v
				break
			}
		}

		_, storeErr := s.storeReceipt(parsed, name, txID)
		if errors.Is(storeErr, errDuplicateReceipt) {
			result.Unchanged++
			continue
		}
		if storeErr != nil {
			result.Skipped++
			result.SkippedFiles = append(result.SkippedFiles, name)
			continue
		}

		result.Imported++
		result.Items += len(parsed.Items)

		if txID != nil {
			result.Linked++
		} else {
			result.Unlinked++
		}
	}

	return result, nil
}

//nolint:noctx // DB.Exec uses background context implicitly
func (s *Service) storeReceipt(r *Receipt, sourceFile string, txID *int64) (int64, error) {
	res, err := s.DB.Exec(
		`INSERT OR IGNORE INTO receipts
		 (store_id, nr_dok, purchased_at, total_cents, paid_cents,
		  discount_cents, deposit_cents, payment_form, item_count, source_file,
		  transaction_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.StoreID, r.NrDok, r.PurchasedAt, r.TotalCents, r.PaidCents,
		r.DiscountCents, r.DepositCents, r.PaymentForm, len(r.Items), sourceFile,
		txID,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting receipt: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("checking rows affected: %w", err)
	}
	if affected == 0 {
		return 0, errDuplicateReceipt
	}
	receiptID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("reading receipt id: %w", err)
	}

	for _, item := range r.Items {
		if _, err := s.DB.Exec(
			`INSERT OR IGNORE INTO receipt_items
			 (receipt_id, name, quantity, unit_price_cents, brutto_cents, vat_class, discount_cents)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			receiptID, item.Name, item.Quantity, item.UnitPriceCents, item.BruttoCents,
			item.VATClass, item.DiscountCents,
		); err != nil {
			return 0, fmt.Errorf("inserting receipt item: %w", err)
		}
	}

	return receiptID, nil
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
