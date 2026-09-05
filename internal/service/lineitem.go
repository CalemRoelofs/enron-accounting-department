// Package service provides business logic for financial operations.
//
//nolint:noctx // deliberate: DB calls use the default background context
package service

import (
	"encoding/json"
	"fmt"
)

const statusSuccess = "success"

type AddLineItemResult struct {
	Status                string `json:"status"`
	LineItemID            int64  `json:"line_item_id"`
	RunningTotalCents     int64  `json:"running_total_cents"`
	TransactionTotalCents int64  `json:"transaction_total_cents"`
	RemainingCents        int64  `json:"remaining_cents"`
}

// CheckLineItemResult holds the result of checking line items.
type CheckLineItemResult struct {
	Status           string `json:"status"`
	DiscrepancyCents int64  `json:"discrepancy_cents"`
	Warning          string `json:"warning,omitempty"`
}

// AddLineItem adds a line item to a transaction.
func (s *Service) AddLineItem(
	txID int64,
	itemName string,
	amountCents int64,
	category string,
	tags []string,
	notes *string,
) (*AddLineItemResult, error) {
	var txTotal int64
	err := s.DB.QueryRow(
		"SELECT amount_cents FROM transactions WHERE id = ?", txID,
	).Scan(&txTotal)
	if err != nil {
		return nil, fmt.Errorf("transaction not found: %w", err)
	}

	tagsJSON, err := json.Marshal(tags)
	if err != nil {
		return nil, fmt.Errorf("marshaling tags: %w", err)
	}

	result, err := s.DB.Exec(
		"INSERT INTO line_items (transaction_id, item_name, amount_cents, category, tags, notes) VALUES (?, ?, ?, ?, ?, ?)",
		txID,
		itemName,
		amountCents,
		category,
		string(tagsJSON),
		notes,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting line item: %w", err)
	}

	lineItemID, err := result.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("getting last insert id: %w", err)
	}

	var runningTotal int64
	err = s.DB.QueryRow(
		"SELECT COALESCE(SUM(amount_cents), 0) FROM line_items WHERE transaction_id = ?",
		txID,
	).Scan(&runningTotal)
	if err != nil {
		return nil, fmt.Errorf("summing line items: %w", err)
	}

	remaining := max(txTotal-runningTotal, 0)

	return &AddLineItemResult{
		Status:                statusSuccess,
		LineItemID:            lineItemID,
		RunningTotalCents:     runningTotal,
		TransactionTotalCents: txTotal,
		RemainingCents:        remaining,
	}, nil
}

// CheckLineItem checks line items against the transaction total.
func (s *Service) CheckLineItem(txID int64) (*CheckLineItemResult, error) {
	var txTotal int64
	err := s.DB.QueryRow(
		"SELECT amount_cents FROM transactions WHERE id = ?", txID,
	).Scan(&txTotal)
	if err != nil {
		return nil, fmt.Errorf("transaction not found: %w", err)
	}

	var lineItemSum int64
	err = s.DB.QueryRow(
		"SELECT COALESCE(SUM(amount_cents), 0) FROM line_items WHERE transaction_id = ?",
		txID,
	).Scan(&lineItemSum)
	if err != nil {
		return nil, fmt.Errorf("summing line items: %w", err)
	}

	discrepancy := max(txTotal-lineItemSum, 0)

	res := &CheckLineItemResult{
		Status:           statusSuccess,
		DiscrepancyCents: discrepancy,
	}

	if discrepancy != 0 {
		const centsPerUnit = 100.0
		txPLN := float64(txTotal) / centsPerUnit
		liPLN := float64(lineItemSum) / centsPerUnit
		discPLN := float64(discrepancy) / centsPerUnit
		res.Warning = fmt.Sprintf(
			"Line items sum to %.2f PLN, transaction is %.2f PLN",
			liPLN, txPLN,
		)
		if discrepancy > 0 {
			res.Warning += fmt.Sprintf(" (short by %.2f PLN)", discPLN)
		}
	}

	return res, nil
}
