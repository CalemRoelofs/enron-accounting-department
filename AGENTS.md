# AGENTS.md — Instructions for AI coding agents

This file is read by AI coding agents (opencode, aider, etc.) working on this repo.
Follow these rules strictly.

## Workflow

1. **Use opencode for all code changes.** Never write Go files directly — no patches, no
   `write_file` on source, no terminal sed/edits. Every code change goes through:
   ```
   opencode run --model openrouter/deepseek/deepseek-v4-flash "<prompt>"
   ```
   OpenCode is configured at `~/.config/opencode/opencode.jsonc` with DeepSeek V4 Flash
   via OpenRouter. The API key is read from `~/.openrouter-key`.

2. **TDD first.** Write the failing test, run it to confirm it fails, then implement.
   Run tests after every change:
   ```
   go test ./... -count=1
   ```

3. **Lint after every change.** `golangci-lint run` must pass with 0 issues before
   committing. The pre-commit hook enforces this.

4. **Commit automatically** when all tests pass and lint is clean. Never leave dirty
   state. Use descriptive commit messages:
   ```
   git add -A && git commit -m "type: short description"
   ```

5. **Push to GitHub** after committing. Remote is `origin` → `main`:
   ```
   git push origin main
   ```

## Code conventions

### API structs

- **Always fetch the actual API documentation** before writing client code. Do not
  guess at response shapes. The Enable Banking API docs are at:
  https://enablebanking.com/docs/api/reference/

- Millennium bank returns `creditor_account.iban: null` with the IBAN hidden in
  `creditor_account.other.identification` (scheme_name: "BBAN"). Use the `.IBAN()`
  method on `AccountResource` which handles this fallback automatically.

### Transactions

- The API returns transactions **newest-first**. Always sort by `BookingDate` ascending
  before processing to ensure pay period rollover works correctly.
- Use `INSERT OR IGNORE` for dedup. A unique index on `(bank_transaction_id, account_id)`
  prevents duplicates.

### Pay periods

- `RollPayPeriod` closes the open period at `salaryDate - 1 day` and opens a new one
  at `salaryDate`. It queries `SELECT date FROM transactions WHERE category = 'Salary'`
  to check the gap between salaries.

## Sensitive data

- `*.pem` is gitignored. Private keys must never be committed.
- `~/.finance-cli/config.json` lives outside the repo — contains IBANs and app secrets.
- Test fixtures use fake IBANs. Never check in real IBANs or personal data.