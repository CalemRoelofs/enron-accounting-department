finance-cli — Personal Finance Tracker Spec
Problem Statement
The user needs a hyper-personalized, zero-friction personal finance tracker tailored to their specific mental models (payday-to-payday timeboxing) and complex multi-account flows (routing money between Bank Millennium, Revolut, and — as categorized destinations only — savings/investment accounts like Trade Republic, IKE, IKZE, and XTB). Traditional budgeting apps enforce strict calendar-month zero-based budgeting and fail to handle transfers between accounts smoothly.
The application must be completely decoupled: the core engine must be a standalone Go CLI tool (finance-cli) that can be executed independently via system cron to ingest Open Banking data, while also serving as a callable utility for the user's Nous Hermes Agent over Telegram. The database must remain local, and all AI inference (e.g., vision receipt reading, natural language translation) is performed externally by the Hermes agent. Because finance-cli is an AI-first backend tool, all command outputs must be strictly well-formed JSON to stdout by default, without human-centric ASCII tables.
None of Trade Republic, IKE, IKZE, or XTB have Open Banking / PSD2 connectivity available to this project. Rather than modeling each as a manually-maintained account with its own balance (a number finance-cli could never verify against anything, and would just drift), money moving to any of them is simply categorized at the point it leaves a synced account. Contribution tracking, interest, and overall portfolio performance for these accounts are already tracked by the user elsewhere and are explicitly out of scope here.
Solution
A local-first Go CLI application (finance-cli) backed by a SQLite database, built using Go 1.27+, [github.com/urfave/cli/v3](https://github.com/urfave/cli/v3), and modernc.org/sqlite (pure-Go driver — no cgo, no toolchain-dependent cross-compilation headaches). Go 1.27's encoding/json (now backed by the encoding/json/v2 implementation) is used for all output serialization.
The CLI acts as the deterministic domain engine and single source of truth:
 * Automated Open Banking Sync: Connects to the Enable Banking API to pull settled PLN transactions from both Bank Millennium and Revolut (personal PSD2 connector LT304580906). Enable Banking is free for personal, own-account production use, which is why it replaces the now-defunct Nordigen/GoCardless free tier. Paginates fully via continuation_key before processing a page.
 * Automated Transfer & Counterparty Matching via IBAN: Automatically identifies transfers moving between the two synced owned accounts (Millennium, Revolut) by matching transaction creditor_account.iban / debtor_account.iban against stored account IBANs, tagging them as Transfer_Internal without inflating income or expenses. Separately, any transaction whose counterparty IBAN matches an entry in known_counterparties from config.json (Trade Republic, IKE, IKZE, XTB, or any future one added the same way) is tagged with that entry's category (e.g., Savings_TR, Savings_IKE) — this is a plain categorization rule per counterparty, not an account-balance update, and nothing in finance-cli tracks a balance for any of them.
 * Payday-to-Payday Dynamic Timeboxing: Automatically closes the active pay period and opens a new one when a transaction matching the Salary detection rule is ingested (rule specified in Implementation Decisions — this fires deterministically at sync time, with no manual categorization step required first).
 * Hermes Agent Integration: Hermes interacts with finance-cli using standard CLI subcommands. Hermes executes AI tasks (vision parsing, text translation) externally and passes structured data to finance-cli flags — never a raw JSON payload blob, so every flag can be individually validated by the CLI layer before it reaches business logic. All commands output well-formed JSON to stdout for seamless parsing by the agent. Hermes also owns proactive reminders that live outside finance-cli itself (PSD2 consent renewal) — see below.
 * Read-Only Text-to-SQL Safety: Hermes answers analytical questions by passing generated SQL to finance-cli query "<sql>", which executes against SQLite using a read-only connection plus an application-level guard (single SELECT-only statement, execution timeout, no ATTACH/PRAGMA). Every other subcommand opens the database read-write, since they all mutate state.
 * Thin CLI, Testable Core & Pre-Execution Config Validation: urfave/cli/v3 is used strictly for argument parsing and validation. Config loading and validation are executed before every CLI invocation to guarantee valid application state prior to subcommand routing. All business logic — IBAN/salary/counterparty matching, pay-period rollover, line-item math, query-guard parsing — lives in an underlying service layer with no dependency on the CLI framework, so it can be exercised directly in tests without shelling out to the compiled binary.
User Stories
 * As a user, I want to run finance-cli sync via a standard system cron job so that my settled bank transactions from both Bank Millennium and Revolut are fetched via the Enable Banking API and saved to SQLite, fully paginated in a single atomic run.
 * As a user, I want finance-cli sync to match counterparty IBANs against my stored accounts table so that transfers between Millennium and Revolut are automatically tagged as Transfer_Internal.
 * As a user, I want finance-cli sync to detect my salary deposit — an INCOMING TRANSFER-type credit from my employer's IBAN — and use it to close the current pay period and open a new one, without me having to categorize anything first.
 * As a user, I want my Telegram-based Hermes agent to execute finance-cli query "SELECT..." safely in read-only mode, which outputs a JSON array of records to stdout for Hermes to summarize.
 * As a user, I want to chat with Hermes to re-categorize a transaction (e.g., "Change transaction 124 to Kids Toys"), which Hermes accomplishes by invoking finance-cli categorize --id 124 --category "Kids" --tags "toys".
 * As a user, I want to upload a receipt photo to Hermes, have Hermes externally extract line items using its own vision model, and then invoke finance-cli lineitem add --id 124 --name "milk" --amount-cents 500 once per line item (well-formed flags, not a JSON payload) to attach the breakdown to the bank transaction.
 * As a user, once Hermes has added every line item from a receipt, I want finance-cli lineitem check --id 124 to output a structured JSON warning if the line items don't sum to the bank transaction total, so Hermes can parse the discrepancy and ask me how to proceed.
 * As a user, I want money leaving Millennium or Revolut toward any of my savings/investment destinations (Trade Republic, IKE, IKZE, XTB) to be automatically categorized per-destination (e.g., Savings_TR, Savings_IKE) so it's excluded from expense totals — without finance-cli maintaining any balance, contribution total, or performance figure for any of them, since I already track that separately myself. Adding a new one is just a new known_counterparties entry in config.json, no code change.
 * As a user, I want all CLI commands to output well-formed JSON by default (no ASCII tables) so my AI agent never fails to parse the response.
 * As a user, I want finance-cli sync to exit with a non-zero exit code and print a JSON error containing a re-auth URL to stdout if an Enable Banking consent has expired — and I want Hermes to proactively warn me a few days before the ~90-day PSD2 consent window lapses, since renewal requires me to personally complete SCA in a browser and cron can't do that unattended.
Implementation Decisions
 * Language & CLI Framework: Go 1.27+, [github.com/urfave/cli/v3](https://github.com/urfave/cli/v3) for subcommand routing and flag parsing (pinned to v3 specifically — no "v2 or v3" ambiguity).
 * Database Driver: modernc.org/sqlite (pinned — pure Go, no cgo, so cross-compiling the cron binary doesn't need a C toolchain on the target).
 * Architecture & Execution Flow: config.json loading and validation run before every CLI invocation. If validation fails, execution halts immediately, returning a non-zero exit code and a JSON error payload. Following successful validation, urfave/cli/v3 commands do argument parsing and flag validation, then call into an internal/service package that holds all business logic and operates on a *sql.DB (or a small interface over it). This keeps the CLI shell out of the way of testing.
 * Testing: Tests instantiate modernc.org/sqlite's :memory: database directly and call service-layer functions against it — no shelling out to the compiled binary. Favors fast, isolated unit tests over slower end-to-end CLI invocations; CLI-level tests are reserved for verifying flag parsing/validation itself.
 * Database Access Mode: Every subcommand except query opens finance.db read-write — this is SQLite's default open mode, so nothing special needs to be requested for it. Only query explicitly forces the read-only connection string file:finance.db?mode=ro. WAL journal mode (PRAGMA journal_mode=WAL) is enabled on the rw connection so a sync write transaction doesn't block a concurrent query read fired by Hermes in a separate process.
 * Database: SQLite3, file stored locally at ~/.finance-cli/finance.db.
   * Schema:
     * accounts: id, name, iban, type, virtual_balance. Only synced accounts (Millennium, Revolut) get a row here.
     * transactions: id, bank_transaction_id, account_id, date, amount_cents (integer), currency (PLN — single-currency scope, matches all observed real flows across Millennium/Revolut; revisit only if a foreign-currency account is added), merchant_name, transfer_title (nullable — mapped 1:1 from Enable Banking's remittance_information_unstructured field at ingestion; renamed to something a human or an LLM prompt can actually parse on sight), creditor_iban, debtor_iban, category, tags (JSON array), notes (nullable, free text), needs_review (bool).
     * line_items: id, transaction_id, item_name, amount_cents, category, tags (JSON array), notes (nullable, free text).
     * pay_periods: id, start_date, end_date (closed on Salary ingestion).
     * bank_connections: id, provider, consent_granted_at, consent_expires_at — used by Hermes to schedule the proactive renewal reminder in story 10. Stays a DB table (not config) because sync writes to it at runtime; it isn't a static tunable.
 * Configuration & Validation: Settings and counterparty mapping definitions are stored in a JSON config file at ~/.finance-cli/config.json. The application validates this configuration before every CLI invocation prior to executing any command logic:
   {
  "employer_iban": "PL541140100000200301001002",
  "salary_min_gap_days": 25,
  "salary_min_amount_cents": null,
  "known_counterparties": [
    {
      "iban": "DE12345678901234567890",
      "label": "Trade Republic",
      "category": "Savings_TR"
    },
    {
      "iban": "PL111122223333444455556666",
      "label": "IKE",
      "category": "Savings_IKE"
    },
    {
      "iban": "PL222233334444555566667777",
      "label": "IKZE",
      "category": "Savings_IKZE"
    },
    {
      "iban": "PL333344445555666677778888",
      "label": "XTB",
      "category": "Savings_XTB"
    }
  ]
}

   Validation Rules: The config loader verifies file existence, valid JSON syntax, valid IBAN formats for employer_iban and all known_counterparties, non-negative values for salary_min_gap_days, and unique IBAN keys across known_counterparties.
 * AI-First Output: All CLI outputs write strictly formatted JSON payloads to stdout using Go 1.27's encoding/json. Non-data logging or diagnostics write to stderr or a log file.
 * Salary Detection Rule: During sync, for each ingested transaction: if amount.value > 0 (a credit) and debtor_account.iban == config.employer_iban, tag category = "Salary". Treat transfer_title matching /Lista Plac \d{2}\/\d{4}/i as a secondary confirmation signal only, not the primary key, since payroll-description wording can change if the employer switches payroll providers. On a Salary tag: close the currently open pay_periods row and open a new one dated from the transaction date — but only if the last Salary-tagged transaction is more than salary_min_gap_days in the past (default 25 days), so a same-employer bonus or reimbursement transfer mid-period doesn't falsely trigger a second rollover.
 * Core Subcommands:
   * finance-cli sync: Fetches settled PLN transactions from Enable Banking for Millennium and Revolut, looping on continuation_key until exhausted. Matches inter-account IBANs (Transfer_Internal) and known_counterparties IBANs loaded from config.json (Trade Republic, IKE, IKZE, XTB, etc.), applies the Salary detection rule and rolls pay periods, all inside a single SQLite transaction so a mid-run failure can't leave partial state. Outputs {"status":"success","ingested":N,"transfers_matched":M,"pay_period_rolled":bool}. On expired consent: exits non-zero with {"status":"error","reason":"consent_expired","reauth_url":"..."}.
   * finance-cli query <sql_string>: Opens SQLite with file:finance.db?mode=ro. Before execution, rejects anything that isn't a single bare SELECT statement (no ;-separated batches, no ATTACH, PRAGMA, or VACUUM), and runs with a bounded execution timeout (default 5s) to guard against pathological LLM-generated queries. Outputs [{"column": "value", ...}].
   * finance-cli categorize --id <tx_id> --category <cat> [--tags <tag1,tag2>] [--notes <text>]: Updates transaction category/tags/notes. Outputs {"status":"success","id":124}.
   * finance-cli lineitem add --id <tx_id> --name <string> --amount-cents <int> [--category <cat>] [--tags <tag1,tag2>] [--notes <text>]: Inserts a single line item under the given transaction, one flag-based call per item (no JSON payload). Outputs {"status":"success","line_item_id":N,"running_total_cents":X,"transaction_total_cents":Y,"remaining_cents":Y-X} so Hermes can see running progress after every item.
   * finance-cli lineitem check --id <tx_id>: Explicit finalize step, called once Hermes has added every parsed line item from a receipt. Outputs {"status":"success","discrepancy_cents":0} if line items sum to the transaction total, or {"status":"success","discrepancy_cents":2000,"warning":"Line items sum to 80.00 PLN, transaction is 100.00 PLN"} otherwise.
 * PSD2 Consent Renewal: Not treated purely as a sync error path. bank_connections.consent_expires_at is checked by a separate lightweight Hermes-side scheduled check (independent of the sync cron cadence) that messages the user proactively a few days before expiry, since renewal requires the user to complete Strong Customer Authentication in a browser — this cannot be done headlessly no matter how the re-auth URL is surfaced.
 * Enable Banking API Data Fixture:
   The Enable Banking API standardizes transaction data across PSD2 endpoints ([https://enablebanking.com/docs/api/reference/](https://enablebanking.com/docs/api/reference/)). The remittance_information_unstructured field below is the raw upstream field name; it's mapped to the transfer_title column at ingestion. Below is the canonical fixture used by finance-cli for TDD testing:
{
  "transactions": [
    {
      "transaction_id": "20260905-MILL-TR-99382",
      "booking_date": "2026-09-05",
      "value_date": "2026-09-05",
      "amount": {
        "value": "-2000.00",
        "currency": "PLN"
      },
      "creditor_name": "JOHN DOE",
      "creditor_account": {
        "iban": "DE12345678901234567890"
      },
      "debtor_account": {
        "iban": "PL987654321098765432109876"
      },
      "remittance_information_unstructured": "Trade Republic Deposit",
      "status": "BOOKED"
    },
    {
      "transaction_id": "20260903-MILL-REV-11223",
      "booking_date": "2026-09-03",
      "value_date": "2026-09-03",
      "amount": {
        "value": "-500.00",
        "currency": "PLN"
      },
      "creditor_name": "JOHN DOE",
      "creditor_account": {
        "iban": "LT304580906123456789"
      },
      "debtor_account": {
        "iban": "PL987654321098765432109876"
      },
      "remittance_information_unstructured": "Top up",
      "status": "BOOKED"
    },
    {
      "transaction_id": "20260827-MILL-SALARY-00417",
      "booking_date": "2026-08-27",
      "value_date": "2026-08-27",
      "amount": {
        "value": "15319.93",
        "currency": "PLN"
      },
      "creditor_name": "JOHN DOE",
      "creditor_account": {
        "iban": "PL987654321098765432109876"
      },
      "debtor_account": {
        "iban": "PL541140100000200301001002"
      },
      "remittance_information_unstructured": "Lista Plac 08/2026",
      "status": "BOOKED"
    }
  ],
  "continuation_key": null
}

