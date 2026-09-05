package service

import "database/sql"

// Service provides business logic for financial operations.
type Service struct {
	DB *sql.DB
}

// NewService creates a new Service.
func NewService(db *sql.DB) *Service {
	return &Service{DB: db}
}
