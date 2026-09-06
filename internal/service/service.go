// Package service provides business logic for financial operations.
//
//nolint:godoclint // package doc is in lineitem.go
package service

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/calemroelofs/enron-accounting-department/internal/enablebanking"
	"github.com/calemroelofs/enron-accounting-department/internal/models"
)

// Service provides business logic for financial operations.
type Service struct {
	DB *sql.DB
}

// NewService creates a new Service.
func NewService(db *sql.DB) *Service {
	return &Service{DB: db}
}

// SessionStarter defines the interface for starting a banking auth session.
type SessionStarter interface {
	CreateAuth(req enablebanking.CreateAuthRequest) (*enablebanking.CreateAuthResponse, error)
}

// SessionAuthorizer defines the interface for authorizing a banking session.
type SessionAuthorizer interface {
	AuthorizeSession(code string) (*enablebanking.AuthorizeSessionResponse, error)
}

// StartAuthSession starts a new banking auth session and saves a pending connection.
//
//nolint:noctx // deliberate: DB.Exec uses background context implicitly
func (s *Service) StartAuthSession(
	starter SessionStarter, aspspName, aspspCountry, redirectURI, state string,
) (string, string, error) {
	validUntil := time.Now().UTC().Add(90 * 24 * time.Hour).Format(time.RFC3339)
	req := enablebanking.CreateAuthRequest{
		Access: enablebanking.CreateAuthAccess{
			ValidUntil: validUntil,
		},
		ASPSP: enablebanking.CreateAuthASPSP{
			Name:    aspspName,
			Country: aspspCountry,
		},
		State:       state,
		RedirectURL: redirectURI,
		PsuType:     "personal",
	}
	resp, err := starter.CreateAuth(req)
	if err != nil {
		return "", "", fmt.Errorf("starting auth session: %w", err)
	}

	// Save to bank_connections with state as the session_id placeholder
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = s.DB.Exec(
		`INSERT INTO bank_connections (provider, session_id, aspsp_id, consent_granted_at, consent_expires_at) VALUES (?, ?, ?, ?, ?)`,
		"enablebanking",
		state,
		aspspName,
		now,
		validUntil,
	)
	if err != nil {
		return "", "", fmt.Errorf("saving bank connection: %w", err)
	}

	return resp.URL, resp.AuthorizationID, nil
}

// CompleteAuthSession authorizes a session and updates the bank connection.
//
//nolint:noctx // deliberate: DB.Exec uses background context implicitly
func (s *Service) CompleteAuthSession(
	authorizer SessionAuthorizer,
	code, authorizationID string,
) (string, string, error) {
	session, err := authorizer.AuthorizeSession(code)
	if err != nil {
		return "", "", fmt.Errorf("authorizing session: %w", err)
	}

	// Determine consent expiry
	expiresAt := ""
	if session.Access != nil && session.Access.ValidUntil != "" {
		expiresAt = session.Access.ValidUntil
	}
	if expiresAt == "" {
		expiresAt = time.Now().UTC().Add(90 * 24 * time.Hour).Format(time.RFC3339) // default 90 days
	}

	// Update the bank connection: set session_id to the real session ID and update expiry
	_, err = s.DB.Exec(
		`UPDATE bank_connections SET session_id = ?, consent_expires_at = ? WHERE session_id = ?`,
		session.SessionID, expiresAt, authorizationID,
	)
	if err != nil {
		return "", "", fmt.Errorf("updating bank connection: %w", err)
	}

	// Store session account external IDs (UUIDs from the API) for use in API calls
	for _, acct := range session.Accounts {
		flat := acct.ToSessionAccount()
		_, err = s.DB.Exec(
			`INSERT INTO accounts (name, iban, external_id) VALUES (?, ?, ?)
			 ON CONFLICT(iban) DO UPDATE SET external_id = excluded.external_id`,
			flat.Product, flat.IBAN, flat.UID,
		)
		if err != nil {
			return "", "", fmt.Errorf("storing session account: %w", err)
		}
	}

	return session.SessionID, expiresAt, nil
}

// ListBankConnections returns all bank connections.
//
//nolint:noctx // deliberate: DB.Query uses background context implicitly
func (s *Service) ListBankConnections() ([]models.BankConnection, error) {
	rows, err := s.DB.Query(
		`SELECT id, provider, session_id, aspsp_id, consent_granted_at, consent_expires_at FROM bank_connections ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying bank connections: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var connections []models.BankConnection
	for rows.Next() {
		var conn models.BankConnection
		var grantedAt, expiresAt string
		if scanErr := rows.Scan(
			&conn.ID,
			&conn.Provider,
			&conn.SessionID,
			&conn.AspspID,
			&grantedAt,
			&expiresAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scanning bank connection: %w", scanErr)
		}
		conn.ConsentGrantedAt, _ = time.Parse(time.RFC3339, grantedAt)
		conn.ConsentExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)
		connections = append(connections, conn)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterating bank connections: %w", iterErr)
	}

	return connections, nil
}
