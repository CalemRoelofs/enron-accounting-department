---
name: enron-accounting-department
description: "Personal finance CLI — sync bank transactions via Enable Banking into SQLite, query, categorize, and track pay periods."
version: 1.0.0
author: Calem Roelofs Hughes
license: MIT
---

# enron-accounting-department

Personal finance CLI that syncs bank transactions from [Enable Banking](https://enablebanking.com) into a local SQLite database. Supports querying, categorization, pay period tracking, and offline replay of API data.

## Prerequisites

- Go 1.27+
- Enable Banking account (free) with an application ID and RSA private key
- Config file at `~/.finance-cli/config.json`

**Config file structure:**
```json
{
  "employer_iban": "PL...",
  "salary_min_gap_days": 25,
  "application_id": "your-app-id",
  "private_key_path": "/path/to/private-key.pem",
  "enable_banking_base_url": "https://api.enablebanking.com",
  "known_counterparties": [
    {"iban": "PL57188010221240629015406164", "label": "Trade Republic", "category": "Savings_TR"}
  ]
}
```

## Build

```bash
go build -o enron-accounting-department ./cmd/enron-accounting-department/
```

## Commands

### `auth` — Authentication

| Subcommand | Description | Key flags |
|---|---|---|
| `aspsps` | List available banks | — |
| `start` | Start auth session | `--aspsp-name`, `--aspsp-country`, `--redirect-uri` |
| `complete` | Complete auth with browser redirect | `--url` (preferred) OR `--code` + `--state` |
| `list` | List active bank connections | — |

**Quick auth flow:**
```bash
./enron-accounting-department auth start --aspsp-name "Bank Millennium" --aspsp-country PL
# → opens a redirect URL in the browser — authorize there
# → browser redirects to enablebanking.com/?code=xxx&state=yyy
# → paste the full URL:
./enron-accounting-department auth complete --url "https://enablebanking.com/?code=xxx&state=yyy"
```

### `sync` — Import transactions

| Flag | Description |
|---|---|
| *(none)* | Sync all accounts from the API |
| `--account ID, -a ID` | Sync only this account ID |
| `--fixture PATH, -f PATH` | Replay from a saved JSON fixture file |
| `--save PATH, -s PATH` | After API sync, save raw response to a fixture file |

**Examples:**
```bash
# Full sync
./enron-accounting-department sync

# Single account
./enron-accounting-department sync --account 17

# Save for offline replay
./enron-accounting-department sync --save /tmp/fixture.json

# Replay offline
./enron-accounting-department sync --fixture /tmp/fixture.json
```

**Rate limits:** 4 API pulls per day per ASPSP. Use `--save` on your first pull, then `--fixture` for the rest.

### `query` — SQL queries

```bash
./enron-accounting-department query "SELECT category, COUNT(*) FROM transactions GROUP BY category"
```

Read-only SELECT queries only.

### `categorize` — Tag transactions

| Flag | Required | Description |
|---|---|---|
| `--id` | Yes | Transaction ID |
| `--category` | Yes | Category label (e.g. "Groceries", "Salary", "Transfer_Internal") |
| `--tags` | No | Comma-separated tags |
| `--notes` | No | Free-text notes |

### `accounts add` — Add accounts

| Flag | Required | Description |
|---|---|---|
| `--name` | Yes | Account name |
| `--iban` | Yes | IBAN |
| `--type` | Yes | Account type (checking, savings, etc.) |

### `lineitem` — Split transactions

| Subcommand | Description |
|---|---|
| `add` | Add a line item to a transaction (`--id`, `--name`, `--amount-cents`, `--category`, `--tags`, `--notes`) |
| `check` | Verify line items sum matches the transaction total (`--id`) |

## Transaction categories

When syncing, transactions are auto-categorized:

- **Transfer_Internal** — both IBANs match known accounts (inter-account transfer)
- **Salary** — positive amount + employer IBAN match + "Lista Plac" in remittance info
- **Savings_*** — counterparty IBAN matches a `known_counterparties` entry
- *(empty)* — everything else, set manually with `categorize`

## Pay periods

Each salary payment rolls over the pay period. A new period starts on the salary date,
the previous one ends the day before. Minimum gap between salaries defaults to 25 days.

## Architecture

```
cmd/enron-accounting-department/  — CLI (urfave/cli/v3)
internal/
  config/       — load/validate config.json
  db/           — SQLite schema (modernc.org/sqlite, no cgo)
  enablebanking/— HTTP client, JWT auth, Enable Banking types
  models/       — Account, Transaction, LineItem, PayPeriod, BankConnection
  output/       — JSON stdout writer
  service/      — sync, auth, salary, pay periods, categorization, matching
```

Pre-commit hook: `golangci-lint --fix` + `go test ./... -count=1`.