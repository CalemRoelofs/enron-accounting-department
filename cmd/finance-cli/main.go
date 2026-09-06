package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/urfave/cli/v3"

	"github.com/calemroelofs/enron-accounting-department/internal/config"
	"github.com/calemroelofs/enron-accounting-department/internal/db"
	"github.com/calemroelofs/enron-accounting-department/internal/enablebanking"
	"github.com/calemroelofs/enron-accounting-department/internal/output"
	"github.com/calemroelofs/enron-accounting-department/internal/service"
)

const txIDUsage = "Transaction ID"

const successStatus = "success"

//nolint:funlen // CLI command definitions are inherently verbose
func buildApp(cfg *config.Config, svc *service.Service) *cli.Command {
	return &cli.Command{
		Name: "finance-cli",
		Commands: []*cli.Command{
			{
				Name:   "sync",
				Usage:  "Sync bank transactions from Enable Banking (API or fixture)",
				Action: syncAction(svc, cfg),
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "fixture",
						Aliases:  []string{"f"},
						Usage:    "Path to Enable Banking API JSON fixture file",
						Required: false,
					},
				},
			},
			{
				Name:      "query",
				Usage:     "Execute a read-only SQL SELECT query",
				ArgsUsage: "<sql>",
				Action:    queryAction(svc),
			},
			{
				Name:   "categorize",
				Usage:  "Categorize a transaction",
				Action: categorizeAction(svc),
				Flags: []cli.Flag{
					&cli.IntFlag{Name: "id", Usage: txIDUsage, Required: true},
					&cli.StringFlag{Name: "category", Usage: "Category label", Required: true},
					&cli.StringFlag{Name: "tags", Usage: "Comma-separated tags", Required: false},
					&cli.StringFlag{Name: "notes", Usage: "Free-text notes", Required: false},
				},
			},
			{
				Name:  "accounts",
				Usage: "Manage accounts",
				Commands: []*cli.Command{
					{
						Name:   "add",
						Usage:  "Add a new account",
						Action: accountsAddAction(svc),
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "name", Usage: "Account name", Required: true},
							&cli.StringFlag{Name: "iban", Usage: "IBAN", Required: true},
							&cli.StringFlag{Name: "type", Usage: "Account type", Required: true},
						},
					},
				},
			},
			{
				Name:  "lineitem",
				Usage: "Manage line items for a transaction",
				Commands: []*cli.Command{
					{
						Name:   "add",
						Usage:  "Add a line item to a transaction",
						Action: lineitemAddAction(svc),
						Flags: []cli.Flag{
							&cli.IntFlag{Name: "id", Usage: txIDUsage, Required: true},
							&cli.StringFlag{Name: "name", Usage: "Item name", Required: true},
							&cli.IntFlag{Name: "amount-cents", Usage: "Amount in cents", Required: true},
							&cli.StringFlag{Name: "category", Usage: "Category label", Required: false},
							&cli.StringFlag{Name: "tags", Usage: "Comma-separated tags", Required: false},
							&cli.StringFlag{Name: "notes", Usage: "Free-text notes", Required: false},
						},
					},
					{
						Name:   "check",
						Usage:  "Check line items sum against transaction total",
						Action: lineitemCheckAction(svc),
						Flags: []cli.Flag{
							&cli.IntFlag{Name: "id", Usage: txIDUsage, Required: true},
						},
					},
				},
			},
			{
				Name:  "auth",
				Usage: "Manage Enable Banking authentication",
				Commands: []*cli.Command{
					{
						Name:   "start",
						Usage:  "Start a new auth session",
						Action: authStartAction(svc, cfg),
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "aspsp-name", Usage: "Bank name from the ASPSP list", Required: true},
							&cli.StringFlag{
								Name:     "aspsp-country",
								Usage:    "Bank country code from the ASPSP list",
								Required: true,
							},
							&cli.StringFlag{Name: "redirect-uri", Usage: "Redirect URI", Required: false},
						},
					},
					{
						Name:   "complete",
						Usage:  "Complete an auth session",
						Action: authCompleteAction(svc, cfg),
						Flags: []cli.Flag{
							&cli.StringFlag{Name: "code", Usage: "Auth code", Required: false},
							&cli.StringFlag{Name: "state", Usage: "Authorization ID from auth start", Required: false},
							&cli.StringFlag{
								Name:     "url",
								Usage:    "Full redirect URL with code and state query params",
								Required: false,
							},
						},
					},
					{
						Name:   "list",
						Usage:  "List bank connections",
						Action: authListAction(svc, cfg),
					},
					{
						Name:   "aspsps",
						Usage:  "List available ASPSPs",
						Action: authAspspsAction(cfg),
					},
				},
			},
		},
	}
}

func syncAction(svc *service.Service, cfg *config.Config) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		fixturePath := cmd.String("fixture")
		if fixturePath != "" {
			data, err := os.ReadFile(fixturePath)
			if err != nil {
				output.WriteError(os.Stdout, fmt.Sprintf("reading fixture: %v", err))
				return nil
			}

			result, err := svc.SyncFromFixture(cfg, data)
			if err != nil {
				output.WriteError(os.Stdout, fmt.Sprintf("sync failed: %v", err))
				return nil
			}
			return output.WriteJSON(os.Stdout, result)
		}

		ebClient, err := newEBClient(cfg)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("creating enablebanking client: %v", err))
			return nil
		}

		result, err := svc.SyncFromAPI(cfg, ebClient)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("sync failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, result)
	}
}

func accountsAddAction(svc *service.Service) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		name := cmd.String("name")
		iban := cmd.String("iban")
		acctType := cmd.String("type")

		_, err := svc.DB.Exec("INSERT INTO accounts (name, iban, type) VALUES (?, ?, ?)", name, iban, acctType)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("add account failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, map[string]any{
			"status": successStatus,
		})
	}
}

func queryAction(svc *service.Service) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		sqlStr := strings.TrimSpace(cmd.Args().First())
		if sqlStr == "" {
			output.WriteError(os.Stdout, "query requires a SQL string argument")
			return nil
		}

		dbPath := dbPathFromEnv()
		result, err := svc.ExecuteQuery(dbPath, sqlStr)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("query failed: %v", err))
			return nil
		}

		fmt.Fprintln(os.Stdout, result)
		return nil
	}
}

func categorizeAction(svc *service.Service) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		id := cmd.Int("id")
		category := cmd.String("category")

		var tags []string
		if tagsStr := cmd.String("tags"); tagsStr != "" {
			tags = strings.Split(tagsStr, ",")
		}

		var notes *string
		if n := cmd.String("notes"); n != "" {
			notes = &n
		}

		if err := svc.CategorizeTransaction(int64(id), category, tags, notes); err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("categorize failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, map[string]any{
			"status": successStatus,
			"id":     id,
		})
	}
}

func lineitemAddAction(svc *service.Service) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		id := cmd.Int("id")
		name := cmd.String("name")
		amountCents := cmd.Int("amount-cents")

		category := cmd.String("category")
		var tags []string
		if tagsStr := cmd.String("tags"); tagsStr != "" {
			tags = strings.Split(tagsStr, ",")
		}
		var notes *string
		if n := cmd.String("notes"); n != "" {
			notes = &n
		}

		result, err := svc.AddLineItem(int64(id), name, int64(amountCents), category, tags, notes)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("add lineitem failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, result)
	}
}

func lineitemCheckAction(svc *service.Service) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		id := cmd.Int("id")

		result, err := svc.CheckLineItem(int64(id))
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("check lineitem failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, result)
	}
}

func authStartAction(svc *service.Service, cfg *config.Config) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		if cfg.ApplicationID == "" {
			output.WriteError(os.Stdout, "application_id not set in config")
			return nil
		}
		if cfg.PrivateKeyPath == "" {
			output.WriteError(os.Stdout, "private_key_path not set in config")
			return nil
		}

		aspspName := cmd.String("aspsp-name")
		aspspCountry := cmd.String("aspsp-country")
		redirectURI := cmd.String("redirect-uri")
		if redirectURI == "" {
			redirectURI = "https://enablebanking.com/"
		}

		state := uuid.NewString()

		ebClient, err := newEBClient(cfg)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("create client: %v", err))
			return nil
		}

		var starter service.SessionStarter = ebClient
		redirectURL, authorizationID, err := svc.StartAuthSession(starter, aspspName, aspspCountry, redirectURI, state)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("auth start failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, map[string]any{
			"redirect_url":     redirectURL,
			"authorization_id": authorizationID,
		})
	}
}

// parseAuthURL extracts code and state from a redirect URL query string.
func parseAuthURL(authURL string) (string, string, error) {
	parsedURL, uErr := url.Parse(authURL)
	if uErr != nil {
		return "", "", uErr
	}
	values := parsedURL.Query()
	return values.Get("code"), values.Get("state"), nil
}

//nolint:gocognit // CLI auth flow has multiple config checks and branches
func authCompleteAction(svc *service.Service, cfg *config.Config) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		if cfg.ApplicationID == "" {
			output.WriteError(os.Stdout, "application_id not set in config")
			return nil
		}
		if cfg.PrivateKeyPath == "" {
			output.WriteError(os.Stdout, "private_key_path not set in config")
			return nil
		}

		code := cmd.String("code")
		authorizationID := cmd.String("state")

		if authURL := cmd.String("url"); authURL != "" {
			c, s, err := parseAuthURL(authURL)
			if err != nil {
				output.WriteError(os.Stdout, fmt.Sprintf("parsing url: %v", err))
				return nil
			}
			if c != "" {
				code = c
			}
			if s != "" {
				authorizationID = s
			}
		}

		if code == "" || authorizationID == "" {
			output.WriteError(os.Stdout, "code and state are required; use --code/--state or --url")
			return nil
		}

		ebClient, err := newEBClient(cfg)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("create client: %v", err))
			return nil
		}

		var authorizer service.SessionAuthorizer = ebClient
		sessionID, consentExpiresAt, err := svc.CompleteAuthSession(authorizer, code, authorizationID)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("auth complete failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, map[string]any{
			"session_id":         sessionID,
			"consent_expires_at": consentExpiresAt,
		})
	}
}

func authListAction(svc *service.Service, _ *config.Config) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, _ *cli.Command) error {
		connections, err := svc.ListBankConnections()
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("auth list failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, connections)
	}
}

func authAspspsAction(cfg *config.Config) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, _ *cli.Command) error {
		if cfg.ApplicationID == "" {
			output.WriteError(os.Stdout, "application_id not set in config")
			return nil
		}
		if cfg.PrivateKeyPath == "" {
			output.WriteError(os.Stdout, "private_key_path not set in config")
			return nil
		}

		ebClient, err := newEBClient(cfg)
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("create client: %v", err))
			return nil
		}

		aspsps, err := ebClient.GetASPSPs()
		if err != nil {
			output.WriteError(os.Stdout, fmt.Sprintf("auth aspsps failed: %v", err))
			return nil
		}

		return output.WriteJSON(os.Stdout, aspsps)
	}
}

func newEBClient(cfg *config.Config) (*enablebanking.Client, error) {
	pemData, err := os.ReadFile(cfg.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading private key: %w", err)
	}

	return enablebanking.NewClient(cfg.ApplicationID, pemData, cfg.EnableBankingBaseURL)
}

func dbPathFromEnv() string {
	if p := os.Getenv("FINANCE_CLI_DB_PATH"); p != "" {
		return p
	}
	return os.ExpandEnv("${HOME}/.finance-cli/finance.db")
}

//nolint:govet // err shadow intentional: outer err from config.Load/InitDB is checked earlier
func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(0)

	cfg, err := config.Load()
	if err != nil {
		output.WriteError(os.Stderr, fmt.Sprintf("config: %v", err))
		os.Exit(1)
	}

	dbPath := dbPathFromEnv()

	database, err := db.InitDB(dbPath)
	if err != nil {
		output.WriteError(os.Stderr, fmt.Sprintf("database: %v", err))
		os.Exit(1)
	}
	defer database.Close()

	svc := service.NewService(database)
	app := buildApp(cfg, svc)

	if err := app.Run(context.Background(), os.Args); err != nil {
		_ = database.Close()
		output.WriteError(os.Stderr, fmt.Sprintf("fatal: %v", err))
		//nolint:gocritic // deferred close on line 220 won't run on Exit; explicit close above
		os.Exit(1)
	}
}
