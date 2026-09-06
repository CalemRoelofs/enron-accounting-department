//nolint:testpackage // accessing unexported functions
package enablebanking

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// generateTestKey creates an RSA private key for testing.
func generateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	return key
}

// pemEncodePrivateKey encodes an RSA private key to PEM.
func pemEncodePrivateKey(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling private key: %v", err)
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	return pem.EncodeToMemory(block)
}

func TestGenerateJWT_ValidFormat(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	appID := "test-app-id"
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	client := &Client{
		appID:      appID,
		privateKey: key,
	}

	token, err := client.generateJWT(now)
	if err != nil {
		t.Fatalf("generateJWT failed: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	// Verify token is base64-url encoded with two dots (three segments)
	parts := countParts(token, '.')
	if parts != 3 {
		t.Errorf("expected 3 dot-separated parts in JWT, got %d", parts)
	}
}

func TestGenerateJWT_DifferentKeys(t *testing.T) {
	t.Parallel()
	key1 := generateTestKey(t)
	key2 := generateTestKey(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	c1 := &Client{appID: "app1", privateKey: key1}
	c2 := &Client{appID: "app1", privateKey: key2}

	tok1, _ := c1.generateJWT(now)
	tok2, _ := c2.generateJWT(now)
	if tok1 == tok2 {
		t.Error("expected different tokens for different keys")
	}
}

func TestDoRequest_Success(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	expectedBody := `{"status":"ok"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		if !hasBearer(r.Header.Get("Authorization")) {
			t.Error("Authorization header is not a Bearer token")
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing Content-Type header")
		}
		_, _ = w.Write([]byte(expectedBody))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	body, err := client.doRequest("GET", "/test", nil)
	if err != nil {
		t.Fatalf("doRequest failed: %v", err)
	}
	if string(body) != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, string(body))
	}
}

func TestDoRequest_ServerError(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	_, err := client.doRequest("GET", "/error", nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestDoRequest_NetworkError(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    "http://127.0.0.1:1",
		httpClient: &http.Client{Timeout: time.Second},
	}

	_, err := client.doRequest("GET", "/test", nil)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}

func TestDoRequest_WithBody(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	type testReq struct {
		Message string `json:"message"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req testReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding body: %v", err)
		}
		if req.Message != "hello" {
			t.Errorf("expected message 'hello', got %q", req.Message)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	body, err := client.doRequest("POST", "/echo", testReq{Message: "hello"})
	if err != nil {
		t.Fatalf("doRequest failed: %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("unexpected body: %s", string(body))
	}
}

func TestNewClient(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	pemData := pemEncodePrivateKey(t, key)

	c, err := NewClient("test-app", pemData, "https://api.enablebanking.com")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.appID != "test-app" {
		t.Errorf("expected appID 'test-app', got %q", c.appID)
	}
	if c.baseURL != "https://api.enablebanking.com" {
		t.Errorf("expected baseURL 'https://api.enablebanking.com', got %q", c.baseURL)
	}
}

func TestNewClient_InvalidKey(t *testing.T) {
	t.Parallel()
	_, err := NewClient("test-app", []byte("invalid-pem"), "https://api.enablebanking.com")
	if err == nil {
		t.Fatal("expected error for invalid PEM key")
	}
}

func TestGetASPSPs(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	aspspResponse := `{"aspsps":[{"id":"bank1","aspsp_data":{"name":"Test Bank","country":"PL"}}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/aspsps" {
			t.Errorf("expected /aspsps, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(aspspResponse))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	aspsps, err := client.GetASPSPs()
	if err != nil {
		t.Fatalf("GetASPSPs failed: %v", err)
	}
	if len(aspsps) != 1 {
		t.Fatalf("expected 1 ASPSP, got %d", len(aspsps))
	}
	if aspsps[0].ID != "bank1" {
		t.Errorf("expected ID 'bank1', got %q", aspsps[0].ID)
	}
	if aspsps[0].Name != "Test Bank" {
		t.Errorf("expected Name 'Test Bank', got %q", aspsps[0].Name)
	}
}

//nolint:gocognit // test complexity is acceptable
func TestCreateAuth(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	authResponse := `{"url":"https://auth.bank.com/authorize","authorization_id":"auth_123"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/auth" {
			t.Errorf("expected /auth, got %s", r.URL.Path)
		}

		// Verify the request body has the expected shape
		var req CreateAuthRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if req.ASPSP.Name != "Test Bank" {
			t.Errorf("expected aspsp.name 'Test Bank', got %q", req.ASPSP.Name)
		}
		if req.ASPSP.Country != "FI" {
			t.Errorf("expected aspsp.country 'FI', got %q", req.ASPSP.Country)
		}
		if req.Access.ValidUntil == "" {
			t.Error("expected access.valid_until to be set")
		}
		if req.State == "" {
			t.Error("expected state to be set")
		}
		if req.RedirectURL == "" {
			t.Error("expected redirect_url to be set")
		}
		if req.PsuType != "personal" {
			t.Errorf("expected psu_type 'personal', got %q", req.PsuType)
		}

		_, _ = w.Write([]byte(authResponse))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	req := CreateAuthRequest{
		Access: CreateAuthAccess{
			ValidUntil: "2026-09-16T12:00:00Z",
		},
		ASPSP: CreateAuthASPSP{
			Name:    "Test Bank",
			Country: "FI",
		},
		State:       "test-state-uuid",
		RedirectURL: "https://myapp.com/callback",
		PsuType:     "personal",
	}

	resp, err := client.CreateAuth(req)
	if err != nil {
		t.Fatalf("CreateAuth failed: %v", err)
	}
	if resp.URL != "https://auth.bank.com/authorize" {
		t.Errorf("expected url 'https://auth.bank.com/authorize', got %q", resp.URL)
	}
	if resp.AuthorizationID != "auth_123" {
		t.Errorf("expected authorization_id 'auth_123', got %q", resp.AuthorizationID)
	}
}

func TestAuthorizeSession(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	sessionResponse := `{
		"session_id":"sess_123",
		"accounts":[{"account_id":{"iban":"PL123456789012345678901234"},"uid":"acc_1","currency":"PLN","product":"Checking"}],
		"aspsp":{"name":"Nordea","country":"FI"},
		"psu_type":"personal",
		"access":{"valid_until":"2026-12-06T12:00:00Z"}
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/sessions" {
			t.Errorf("expected /sessions, got %s", r.URL.Path)
		}

		// Verify body contains code
		var req AuthorizeSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if req.Code != "test-code" {
			t.Errorf("expected code 'test-code', got %q", req.Code)
		}

		_, _ = w.Write([]byte(sessionResponse))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	resp, err := client.AuthorizeSession("test-code")
	if err != nil {
		t.Fatalf("AuthorizeSession failed: %v", err)
	}
	if resp.SessionID != "sess_123" {
		t.Errorf("expected session_id 'sess_123', got %q", resp.SessionID)
	}
	if len(resp.Accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(resp.Accounts))
	}
	if resp.Accounts[0].AccountID.IBAN != "PL123456789012345678901234" {
		t.Errorf("expected IBAN 'PL123456789012345678901234', got %q", resp.Accounts[0].AccountID.IBAN)
	}
	if resp.Accounts[0].UID != "acc_1" {
		t.Errorf("expected uid 'acc_1', got %q", resp.Accounts[0].UID)
	}
}

func TestGetTransactions_SinglePage(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	transactionsResponse := `{
		"transactions": [
			{
				"entry_reference": "tx1",
				"booking_date": "2026-09-05",
				"value_date": "2026-09-05",
				"transaction_amount": {"amount": "-200.00", "currency": "PLN"},
				"creditor": {"name": "Shop"},
				"creditor_account": {"iban": "PL111111111111111111111111"},
				"debtor_account": {"iban": "PL222222222222222222222222"},
				"credit_debit_indicator": "DBIT",
				"status": "BOOKED"
			}
		]
	}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/acc_1/transactions" {
			t.Errorf("expected /accounts/acc_1/transactions, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(transactionsResponse))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	txs, contKey, err := client.GetTransactions("acc_1", "", time.Time{})
	if err != nil {
		t.Fatalf("GetTransactions failed: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}
	if txs[0].TransactionID() != "tx1" {
		t.Errorf("expected tx_id 'tx1', got %q", txs[0].TransactionID())
	}
	if contKey != nil {
		t.Errorf("expected nil continuation_key, got %v", *contKey)
	}
}

func TestGetTransactions_WithContinuationKey(t *testing.T) {
	t.Parallel()
	key := generateTestKey(t)
	ock := "next-page-key"
	transactionsResponse := fmt.Sprintf(`{
		"transactions": [
			{
				"entry_reference": "tx2",
				"booking_date": "2026-09-05",
				"value_date": "2026-09-05",
				"transaction_amount": {"amount": "-100.00", "currency": "PLN"},
				"creditor": {"name": "Store"},
				"creditor_account": {"iban": "PL111111111111111111111111"},
				"debtor_account": {"iban": "PL222222222222222222222222"},
				"credit_debit_indicator": "DBIT",
				"status": "BOOKED"
			}
		],
		"continuation_key": "%s"
	}`, ock)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ck := r.URL.Query().Get("continuation_key")
		if ck != ock {
			t.Errorf("expected continuation_key %q, got %q", ock, ck)
		}
		_, _ = w.Write([]byte(transactionsResponse))
	}))
	defer server.Close()

	client := &Client{
		appID:      "test-app",
		privateKey: key,
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	txs, contKey, err := client.GetTransactions("acc_1", ock, time.Time{})
	if err != nil {
		t.Fatalf("GetTransactions failed: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 transaction, got %d", len(txs))
	}
	if contKey == nil {
		t.Fatal("expected non-nil continuation_key")
	}
	if *contKey != ock {
		t.Errorf("expected continuation_key %q, got %q", ock, *contKey)
	}
}

// countParts counts the number of segments in a string separated by sep.
func countParts(s string, sep rune) int {
	n := 0
	for _, c := range s {
		if c == sep {
			n++
		}
	}
	return n + 1
}

// hasBearer checks if the Authorization header is a Bearer token.
func hasBearer(auth string) bool {
	const bearerLen = 7
	return len(auth) > bearerLen && auth[:bearerLen] == "Bearer "
}

// TestEnableBankingAccountIBAN covers both the direct IBAN path and the
// Millennium-style fallback through other.identification.
func TestEnableBankingAccountIBAN(t *testing.T) {
	t.Parallel()

	t.Run("direct iban returns it", func(t *testing.T) {
		t.Parallel()
		acc := EnableBankingAccount{IBANRaw: "PL123456789012345678901234"}
		if got := acc.IBAN(); got != "PL123456789012345678901234" {
			t.Errorf("expected PL IBAN, got %q", got)
		}
	})

	t.Run("iban empty uses other", func(t *testing.T) {
		t.Parallel()
		acc := EnableBankingAccount{
			Other: &OtherIdentification{
				Identification: "PL987654321098765432109876",
				SchemeName:     "BBAN",
			},
		}
		if got := acc.IBAN(); got != "PL987654321098765432109876" {
			t.Errorf("expected PL IBAN from other, got %q", got)
		}
	})

	t.Run("iban and other both empty", func(t *testing.T) {
		t.Parallel()
		acc := EnableBankingAccount{}
		if got := acc.IBAN(); got != "" {
			t.Errorf("expected empty IBAN, got %q", got)
		}
	})

	t.Run("iban takes priority over other", func(t *testing.T) {
		t.Parallel()
		acc := EnableBankingAccount{
			IBANRaw: "PL111111111111111111111111",
			Other: &OtherIdentification{
				Identification: "PL222222222222222222222222",
				SchemeName:     "BBAN",
			},
		}
		if got := acc.IBAN(); got != "PL111111111111111111111111" {
			t.Errorf("expected direct IBAN to take priority, got %q", got)
		}
	})

	t.Run("unmarshal direct iban", func(t *testing.T) {
		t.Parallel()
		data := `{"iban":"PL333333333333333333333333"}`
		var acc EnableBankingAccount
		if err := json.Unmarshal([]byte(data), &acc); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got := acc.IBAN(); got != "PL333333333333333333333333" {
			t.Errorf("expected PL IBAN, got %q", got)
		}
	})

	t.Run("unmarshal millennium style (other.identification)", func(t *testing.T) {
		t.Parallel()
		data := `{"iban":null,"other":{"identification":"PL444444444444444444444444","scheme_name":"BBAN"}}`
		var acc EnableBankingAccount
		if err := json.Unmarshal([]byte(data), &acc); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if got := acc.IBAN(); got != "PL444444444444444444444444" {
			t.Errorf("expected PL IBAN from other.identification, got %q", got)
		}
	})
}
