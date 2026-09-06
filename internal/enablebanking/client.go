//nolint:noctx // deliberate: HTTP calls use background context
package enablebanking

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// defaultTimeout is the default HTTP client timeout.
	defaultTimeout = 30 * time.Second
	// tokenExpiry is the JWT token lifetime.
	tokenExpiry = 5 * time.Minute
	// defaultBaseURL is the default Enable Banking API base URL.
	defaultBaseURL = "https://api.enablebanking.com"
	// apiPrefix is the API path prefix.
	apiPrefix = ""
)

// Client is an HTTP client for the Enable Banking API.
type Client struct {
	appID      string
	privateKey *rsa.PrivateKey
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Enable Banking API client.
func NewClient(appID string, pemKey []byte, baseURL string) (*Client, error) {
	block, _ := pem.Decode(pemKey)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM key block")
	}

	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}

	rsaKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("key is not an RSA private key")
	}

	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return &Client{
		appID:      appID,
		privateKey: rsaKey,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}, nil
}

// generateJWT creates an RS256 JWT for API authentication.
func (c *Client) generateJWT(now time.Time) (string, error) {
	claims := jwt.MapClaims{
		"iss": "enablebanking.com",
		"sub": c.appID,
		"aud": "api.enablebanking.com",
		"iat": now.Unix(),
		"exp": now.Add(tokenExpiry).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["typ"] = "JWT"
	token.Header["kid"] = c.appID

	tokenString, err := token.SignedString(c.privateKey)
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}

	return tokenString, nil
}

// parseRetryAfter parses the Retry-After header value into a duration.
func parseRetryAfter(header string) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}
	// Try as HTTP-date (not common but possible)
	if t, err := time.Parse(http.TimeFormat, header); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d, true
		}
		return 0, false
	}
	// Try as seconds (most common)
	if secs, err := strconv.Atoi(header); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second, true
	}
	return 0, false
}

// responseResult captures the outcome of processing an HTTP response.
type responseResult struct {
	body  []byte
	retry bool
	wait  time.Duration
	err   error
}

// doRequest performs an authenticated HTTP request to the Enable Banking API.
//
//nolint:gocognit // complexity is inherent to the retry loop
func (c *Client) doRequest(method, path string, body any) ([]byte, error) {
	token, err := c.generateJWT(time.Now())
	if err != nil {
		return nil, fmt.Errorf("generating JWT: %w", err)
	}

	var reqBody []byte
	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
	}

	reqURL := c.baseURL + apiPrefix + path

	req, err := c.newRequest(method, reqURL, body, reqBody, token)
	if err != nil {
		return nil, err
	}

	const maxRetries = 5
	const backoffBase = 5 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if body != nil && attempt > 0 {
			req, err = c.newRequest(method, reqURL, body, reqBody, token)
			if err != nil {
				return nil, err
			}
		}

		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			return nil, fmt.Errorf("executing request: %w", doErr)
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("reading response body: %w", readErr)
		}

		if method == "GET" {
			fmt.Fprintf(os.Stderr, "--- DEBUG [%s %s] ---\n%s\n--- END DEBUG ---\n", method, path, string(respBody))
		}

		res := c.processResponse(respBody, resp.StatusCode, resp.Header, attempt, maxRetries, backoffBase)
		if res.err != nil {
			return nil, res.err
		}
		if !res.retry {
			return res.body, nil
		}
		fmt.Fprintf(os.Stderr, "Rate limited (429) on %s %s, retrying in %v (attempt %d/%d)...\n",
			method, path, res.wait, attempt+1, maxRetries)
		time.Sleep(res.wait)
	}

	return nil, fmt.Errorf("unexpected: exceeded max retries without returning")
}

// processResponse determines how to proceed: returns an error, a final body, or a retry signal.
func (c *Client) processResponse(
	respBody []byte,
	statusCode int,
	header http.Header,
	attempt, maxRetries int,
	baseDelay time.Duration,
) responseResult {
	if statusCode != http.StatusTooManyRequests {
		if statusCode < 200 || statusCode >= 300 {
			return responseResult{err: fmt.Errorf("API error (status %d): %s", statusCode, string(respBody))}
		}
		return responseResult{body: respBody}
	}
	if attempt == maxRetries {
		return responseResult{err: fmt.Errorf("API error (status %d): %s", statusCode, string(respBody))}
	}
	return responseResult{retry: true, wait: c.backoffDelay(attempt, baseDelay, header)}
}

// newRequest creates an HTTP request with authentication headers.
func (c *Client) newRequest(method, reqURL string, body any, reqBody []byte, token string) (*http.Request, error) {
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, reqURL, bytes.NewReader(reqBody))
	} else {
		req, err = http.NewRequest(method, reqURL, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// backoffDelay returns the wait duration before the next retry, preferring
// the Retry-After header over exponential backoff.
func (c *Client) backoffDelay(attempt int, baseDelay time.Duration, header http.Header) time.Duration {
	wait := baseDelay * time.Duration(1<<uint(attempt)) // 1s, 2s, 4s, 8s, 16s
	if retryAfter := header.Get("Retry-After"); retryAfter != "" {
		if d, ok := parseRetryAfter(retryAfter); ok {
			wait = d
		}
	}
	return wait
}

// GetASPSPs retrieves the list of available ASPSPs.
//
//nolint:govet // intentional err shadowing
func (c *Client) GetASPSPs() ([]ASPSP, error) {
	body, err := c.doRequest("GET", "/aspsps", nil)
	if err != nil {
		return nil, fmt.Errorf("getting ASPSPs: %w", err)
	}

	var resp ASPSPsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing ASPSPs response: %w", err)
	}

	// The Enable Banking API returns ASPSP data in a nested structure:
	// {"aspsps":[{"id":"bank1","aspsp_data":{"name":"Test Bank","country":"PL"}}]}
	// Promote nested fields up to the top-level ASPSP fields for convenience.
	for i := range resp.ASPSPs {
		aspsp := &resp.ASPSPs[i]
		if aspsp.ASPSPData != nil {
			if aspsp.Name == "" && aspsp.ASPSPData.Name != "" {
				aspsp.Name = aspsp.ASPSPData.Name
			}
			if aspsp.Country == "" && aspsp.ASPSPData.Country != "" {
				aspsp.Country = aspsp.ASPSPData.Country
			}
			if aspsp.ID == "" && aspsp.ASPSPData.ID != "" {
				aspsp.ID = aspsp.ASPSPData.ID
			}
		}
	}

	return resp.ASPSPs, nil
}

// CreateAuth initiates a new authorization session with an ASPSP via POST /auth.
//
//nolint:govet // intentional err shadowing
func (c *Client) CreateAuth(req CreateAuthRequest) (*CreateAuthResponse, error) {
	body, err := c.doRequest("POST", "/auth", req)
	if err != nil {
		return nil, fmt.Errorf("creating auth session: %w", err)
	}

	var resp CreateAuthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing auth response: %w", err)
	}

	return &resp, nil
}

// AuthorizeSession authorizes a session using an auth code via POST /sessions.
//
//nolint:govet // intentional err shadowing
func (c *Client) AuthorizeSession(code string) (*AuthorizeSessionResponse, error) {
	req := AuthorizeSessionRequest{Code: code}
	body, err := c.doRequest("POST", "/sessions", req)
	if err != nil {
		return nil, fmt.Errorf("authorizing session: %w", err)
	}

	var resp AuthorizeSessionResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing session response: %w", err)
	}

	return &resp, nil
}

// GetTransactions retrieves transactions for a given account, with optional
// continuation_key pagination and dateFrom filter.
func (c *Client) GetTransactions(
	accountID, continuationKey string,
	dateFrom time.Time,
) ([]Transaction, *string, error) {
	path := "/accounts/" + url.PathEscape(accountID) + "/transactions"

	params := url.Values{}
	if continuationKey != "" {
		params.Set("continuation_key", continuationKey)
	}
	if !dateFrom.IsZero() {
		params.Set("date_from", dateFrom.Format("2006-01-02"))
	}

	if len(params) > 0 {
		path += "?" + params.Encode()
	}

	body, err := c.doRequest("GET", path, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("getting transactions: %w", err)
	}

	var resp TransactionsResponse
	if uErr := json.Unmarshal(body, &resp); uErr != nil {
		return nil, nil, fmt.Errorf("parsing transactions response: %w", uErr)
	}

	return resp.Transactions, resp.ContinuationKey, nil
}
