# enron-accounting-department

Personal finance CLI — syncs bank transactions via [Enable Banking](https://enablebanking.com), stores them in SQLite, and provides querying, categorization, and pay-period tracking.

## Setup

### 1. Enable Banking application

1. Register at [enablebanking.com](https://enablebanking.com)
2. Create an application to get an application ID
3. Generate an RSA key pair and upload the public certificate:
   ```bash
   openssl genrsa -out private.key 2048
   openssl req -new -x509 -days 365 -key private.key -out public.crt
   ```
4. Place the private key somewhere safe and reference it in config

### 2. Configuration

Create `~/.finance-cli/config.json`:

```json
{
  "employer_iban": "PL54114010100000200301001002",
  "salary_min_gap_days": 25,
  "application_id": "your-app-id",
  "private_key_path": "/path/to/private-key.pem",
  "enable_banking_base_url": "https://api.enablebanking.com",
  "known_counterparties": [
    {"iban": "PL57188010221240629015406164", "label": "Trade Republic", "category": "Savings_TR"},
    {"iban": "PL73124020929927000052549055", "label": "XTB", "category": "Savings_XTB"}
  ]
}
```

### 3. Build

```bash
go build -o enron-accounting-department ./cmd/enron-accounting-department/
```

## Usage

### Authentication

```bash
# List available banks
./enron-accounting-department auth aspsps

# Start auth — opens a URL to authorize in your browser
./enron-accounting-department auth start --aspsp-name "Bank Millennium" --aspsp-country PL

# After authorizing, paste the redirect URL
./enron-accounting-department auth complete --url "https://enablebanking.com/?code=xxx&state=yyy"

# List bank connections
./enron-accounting-department auth list
```

### Sync transactions

```bash
# Sync all accounts
./enron-accounting-department sync

# Sync a single account
./enron-accounting-department sync --account 17

# Save API response to a fixture file
./enron-accounting-department sync --save /tmp/today-fixture.json

# Replay from saved fixture (no API call)
./enron-accounting-department sync --fixture /tmp/today-fixture.json
```

### Query

```bash
./enron-accounting-department query "SELECT category, COUNT(*) FROM transactions GROUP BY category"
```

### Rate limits

Enable Banking allows **4 API pulls per ASPSP per day**. Use `--save` on first pull, then `--fixture` for offline replay. The `--account` flag lets you target specific accounts so you can distribute pulls across ASPSPs.

## Architecture

```
cmd/enron-accounting-department/  — CLI entry point
internal/
  config/        — config loading, IBAN validation
  db/            — SQLite schema and migrations
  enablebanking/ — HTTP client, JWT auth, API types
  models/        — domain types
  output/        — JSON output helpers
  service/       — sync, auth, salary detection, categorization
```

Pre-commit hook runs `golangci-lint` (70+ linters) and `go test` before every commit.