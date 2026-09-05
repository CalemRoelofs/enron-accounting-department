package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/calemroelofs/enron-accounting-department/internal/db"
)

const queryTimeout = 5 * time.Second

// ExecuteQuery runs a read-only SQL query and returns JSON results.
func (s *Service) ExecuteQuery(dbPath, sqlStr string) (string, error) {
	if err := GuardSQL(sqlStr); err != nil {
		return "", err
	}

	roDB, err := db.InitDBReadOnly(dbPath)
	if err != nil {
		return "", fmt.Errorf("opening read-only db: %w", err)
	}
	defer roDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	rows, err := roDB.QueryContext(ctx, sqlStr)
	if err != nil {
		return "", fmt.Errorf("query execution failed: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("getting columns: %w", err)
	}

	var results []map[string]any
	var scanErr error
	for rows.Next() {
		values := make([]any, len(columns))
		valuePtrs := make([]any, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if scanErr = rows.Scan(valuePtrs...); scanErr != nil {
			return "", fmt.Errorf("scanning row: %w", scanErr)
		}

		row := make(map[string]any, len(columns))
		for i, col := range columns {
			val := values[i]
			if b, ok := val.([]byte); ok {
				val = string(b)
			}
			row[col] = val
		}
		results = append(results, row)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return "", fmt.Errorf("rows error: %w", rowsErr)
	}

	if results == nil {
		results = []map[string]any{}
	}

	out, err := json.Marshal(results)
	if err != nil {
		return "", fmt.Errorf("marshaling results: %w", err)
	}

	return string(out), nil
}
