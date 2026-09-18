package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// defaultCurrency is used when an imported order omits its currency.
const defaultCurrency = "PLN"

// Order represents a single imported online order.
type Order struct {
	Source            string        `json:"source"`
	OrderRef          string        `json:"order_ref"`
	OrderDate         string        `json:"order_date"`
	PaidCents         int64         `json:"paid_cents"`
	Currency          string        `json:"currency"`
	Seller            *string       `json:"seller"`
	PaymentMethod     *string       `json:"payment_method"`
	DeliveryCents     int64         `json:"delivery_cents"`
	BankTransactionID *int64        `json:"bank_transaction_id"`
	Items             []OrderItem   `json:"items"`
	Refunds           []OrderRefund `json:"refunds"`
}

// OrderItem represents a single product line on an order.
type OrderItem struct {
	Name       string  `json:"name"`
	Code       *string `json:"code"`
	Quantity   *string `json:"quantity"`
	UnitCents  *int64  `json:"unit_cents"`
	GrossCents int64   `json:"gross_cents"`
}

// OrderRefund represents a refund issued against an order.
type OrderRefund struct {
	RefundRef   *string `json:"refund_ref"`
	AmountCents int64   `json:"amount_cents"`
	RefundDate  *string `json:"refund_date"`
	Partial     int     `json:"partial"`
}

// ImportOrdersResult is the result of an import-orders run.
type ImportOrdersResult struct {
	Imported     int      `json:"imported"`
	Items        int      `json:"items"`
	Refunds      int      `json:"refunds"`
	Unchanged    int      `json:"unchanged"`
	Skipped      int      `json:"skipped"`
	SkippedFiles []string `json:"skipped_files"`
	Linked       int      `json:"linked"`
	Unlinked     int      `json:"unlinked"`
}

// ImportOrders reads every JSON file in dir, each expected to hold an array of
// orders, stores new orders with their items and refunds, and links them to an
// existing transaction when possible. Malformed files are skipped, never fatal.
func (s *Service) ImportOrders(dir string) (ImportOrdersResult, error) {
	var result ImportOrdersResult

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		return result, fmt.Errorf("reading orders dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		data, readErr := os.ReadFile(filepath.Join(dir, name))
		if readErr != nil {
			result.Skipped++
			result.SkippedFiles = append(result.SkippedFiles, name)
			continue
		}

		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 || !bytes.HasPrefix(trimmed, []byte("[")) {
			result.Skipped++
			result.SkippedFiles = append(result.SkippedFiles, name)
			continue
		}

		var orders []Order
		if jsonErr := json.Unmarshal(trimmed, &orders); jsonErr != nil {
			result.Skipped++
			result.SkippedFiles = append(result.SkippedFiles, name)
			continue
		}

		storeOrders(s, orders, name, &result)
	}

	return result, nil
}

// storeOrders inserts each parsed order and folds the outcome into result.
func storeOrders(s *Service, orders []Order, name string, result *ImportOrdersResult) {
	for i := range orders {
		imported, linked, storeErr := s.storeOrder(&orders[i])
		if storeErr != nil {
			result.Skipped++
			result.SkippedFiles = append(result.SkippedFiles, name)
			return
		}
		if !imported {
			result.Unchanged++
			continue
		}
		result.Imported++
		result.Items += len(orders[i].Items)
		result.Refunds += len(orders[i].Refunds)
		if linked {
			result.Linked++
		} else {
			result.Unlinked++
		}
	}
}

// storeOrder inserts one order with its items and refunds. It reports whether
// the order was newly inserted and whether it was linked to a transaction.
//
//nolint:noctx // DB operations use background context implicitly
func (s *Service) storeOrder(order *Order) (bool, bool, error) {
	currency := order.Currency
	if currency == "" {
		currency = defaultCurrency
	}

	var txID *int64
	if order.BankTransactionID != nil {
		var found int64
		lookupErr := s.DB.QueryRow(
			"SELECT id FROM transactions WHERE id = ?", *order.BankTransactionID,
		).Scan(&found)
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return false, false, fmt.Errorf("looking up transaction: %w", lookupErr)
		}
		if lookupErr == nil {
			txID = &found
		}
	}

	res, err := s.DB.Exec(
		`INSERT OR IGNORE INTO orders
		 (source, order_ref, order_date, paid_cents, refunded_cents, currency,
		  seller, payment_method, delivery_cents, transaction_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		order.Source, order.OrderRef, order.OrderDate, order.PaidCents, sumRefunds(order.Refunds),
		currency, order.Seller, order.PaymentMethod, order.DeliveryCents, txID,
	)
	if err != nil {
		return false, false, fmt.Errorf("inserting order: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, false, fmt.Errorf("checking rows affected: %w", err)
	}
	if affected == 0 {
		return false, false, nil
	}
	orderID, err := res.LastInsertId()
	if err != nil {
		return false, false, fmt.Errorf("reading order id: %w", err)
	}

	for _, item := range order.Items {
		if _, itemErr := s.DB.Exec(
			`INSERT OR IGNORE INTO order_items
			 (order_id, name, code, quantity, unit_price_cents, gross_cents)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			orderID, item.Name, item.Code, item.Quantity, item.UnitCents, item.GrossCents,
		); itemErr != nil {
			return false, false, fmt.Errorf("inserting order item: %w", itemErr)
		}
	}

	for _, refund := range order.Refunds {
		if _, refundErr := s.DB.Exec(
			`INSERT OR IGNORE INTO refunds
			 (order_id, refund_ref, amount_cents, refund_date, partial)
			 VALUES (?, ?, ?, ?, ?)`,
			orderID, refund.RefundRef, refund.AmountCents, refund.RefundDate, refund.Partial,
		); refundErr != nil {
			return false, false, fmt.Errorf("inserting refund: %w", refundErr)
		}
	}

	return true, txID != nil, nil
}

// sumRefunds returns the total amount refunded across the given refunds.
func sumRefunds(refunds []OrderRefund) int64 {
	var total int64
	for _, refund := range refunds {
		total += refund.AmountCents
	}
	return total
}
