//nolint:revive // EnableBanking prefix intentional for disambiguation
package enablebanking

// ASPSP represents an Account Servicing Payment Service Provider (bank).
type ASPSP struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Country   string     `json:"country,omitempty"`
	ASPSPData *ASPSPData `json:"aspsp_data,omitempty"`
}

// ASPSPData holds the detailed data for an ASPSP, returned nested inside the
// ASPSP object by the Enable Banking API.
type ASPSPData struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Country string `json:"country,omitempty"`
}

// CreateAuthAccess holds the access validity period for auth requests.
type CreateAuthAccess struct {
	ValidUntil string `json:"valid_until"`
}

// CreateAuthASPSP identifies the ASPSP for auth requests by name and country.
type CreateAuthASPSP struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

// CreateAuthRequest represents a request to POST /auth to start authorization.
type CreateAuthRequest struct {
	Access      CreateAuthAccess `json:"access"`
	ASPSP       CreateAuthASPSP  `json:"aspsp"`
	State       string           `json:"state"`
	RedirectURL string           `json:"redirect_url"`
	PsuType     string           `json:"psu_type,omitempty"`
}

// CreateAuthResponse represents the response from POST /auth.
type CreateAuthResponse struct {
	URL             string `json:"url"`
	AuthorizationID string `json:"authorization_id"`
}

// AuthorizeSessionRequest represents a request to POST /sessions.
type AuthorizeSessionRequest struct {
	Code string `json:"code"`
}

// AccountIdentification represents a nested account identifier, e.g. iban.
type AccountIdentification struct {
	IBAN string `json:"iban"`
}

// AccountResource represents a bank account resource returned by the API.
type AccountResource struct {
	AccountID *AccountIdentification `json:"account_id,omitempty"`
	UID       string                 `json:"uid"`
	Name      string                 `json:"name,omitempty"`
	Product   string                 `json:"product,omitempty"`
	Currency  string                 `json:"currency,omitempty"`
}

// SessionAccount represents a bank account in a session response.
type SessionAccount struct {
	UID      string `json:"uid"`
	IBAN     string `json:"iban"`
	Currency string `json:"currency,omitempty"`
	Product  string `json:"product,omitempty"`
}

// ToSessionAccount converts AccountResource to a flat SessionAccount.
func (r AccountResource) ToSessionAccount() SessionAccount {
	sa := SessionAccount{UID: r.UID, Currency: r.Currency, Product: r.Product}
	if r.AccountID != nil {
		sa.IBAN = r.AccountID.IBAN
	}
	return sa
}

// AuthorizeSessionResponse represents the response from POST /sessions and
// GET /sessions/{id} (both endpoints return the same account resource shape).
type AuthorizeSessionResponse struct {
	SessionID string            `json:"session_id"`
	Accounts  []AccountResource `json:"accounts"`
	ASPSP     *ASPSP            `json:"aspsp,omitempty"`
	PsuType   string            `json:"psu_type,omitempty"`
	Access    *CreateAuthAccess `json:"access,omitempty"`
}

// Session represents a banking session for internal DB usage.
type Session struct {
	SessionID        string           `json:"session_id"`
	Status           string           `json:"status"`
	ASPSPID          string           `json:"aspsp_id,omitempty"`
	ConsentExpiresAt string           `json:"consent_expires_at,omitempty"`
	Accounts         []SessionAccount `json:"accounts,omitempty"`
}

// EnableBankingAmount represents a monetary amount in Enable Banking format.
//
//nolint:revive // stuttering is intentional to avoid package-qualified ambiguity
type EnableBankingAmount struct {
	Value    string `json:"amount"`
	Currency string `json:"currency"`
}

// OtherIdentification represents an alternative account identifier, used when
// the IBAN is not directly provided (e.g. Millennium's "BBAN" scheme).
type OtherIdentification struct {
	Identification string  `json:"identification"`
	SchemeName     string  `json:"scheme_name"`
	Issuer         *string `json:"issuer,omitempty"`
}

// EnableBankingAccount represents a bank account in Enable Banking format.
//
//nolint:revive // stuttering is intentional to avoid package-qualified ambiguity
type EnableBankingAccount struct {
	IBANRaw string               `json:"iban"`
	Other   *OtherIdentification `json:"other,omitempty"`
}

// IBAN returns the account IBAN, falling back to other.identification when the
// direct iban field is empty (Millennium-style responses).
func (a EnableBankingAccount) IBAN() string {
	if a.IBANRaw != "" {
		return a.IBANRaw
	}
	if a.Other != nil {
		return a.Other.Identification
	}
	return ""
}

// PartyIdentification represents a party in a transaction.
type PartyIdentification struct {
	Name string `json:"name,omitempty"`
}

// Transaction represents a bank transaction from the Enable Banking API.
type Transaction struct {
	EntryReference        string               `json:"entry_reference"`
	BookingDate           string               `json:"booking_date"`
	ValueDate             string               `json:"value_date"`
	TransactionAmount     EnableBankingAmount  `json:"transaction_amount"`
	Creditor              *PartyIdentification `json:"creditor,omitempty"`
	CreditorAccount       EnableBankingAccount `json:"creditor_account"`
	Debtor                *PartyIdentification `json:"debtor,omitempty"`
	DebtorAccount         EnableBankingAccount `json:"debtor_account"`
	RemittanceInformation []string             `json:"remittance_information,omitempty"`
	CreditDebitIndicator  string               `json:"credit_debit_indicator"`
	Status                string               `json:"status"`
}

// TransactionID returns the transaction ID, preferring entry_reference.
func (t Transaction) TransactionID() string {
	return t.EntryReference
}

// RemittanceInfo returns the first remittance information string.
func (t Transaction) RemittanceInfo() string {
	if len(t.RemittanceInformation) > 0 {
		return t.RemittanceInformation[0]
	}
	return ""
}

// MerchantName returns the counterparty name.
func (t Transaction) MerchantName() string {
	if t.Creditor != nil && t.Creditor.Name != "" {
		return t.Creditor.Name
	}
	if t.Debtor != nil && t.Debtor.Name != "" {
		return t.Debtor.Name
	}
	return ""
}

// TransactionsResponse represents the response from the transactions endpoint.
type TransactionsResponse struct {
	Transactions    []Transaction `json:"transactions"`
	ContinuationKey *string       `json:"continuation_key,omitempty"`
}

// ASPSPsResponse represents the response from the ASPSPs endpoint.
type ASPSPsResponse struct {
	ASPSPs []ASPSP `json:"aspsps"`
}
