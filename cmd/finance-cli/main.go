package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/calemroelofs/enron-accounting-department/internal/config"
	"github.com/calemroelofs/enron-accounting-department/internal/db"
	"github.com/calemroelofs/enron-accounting-department/internal/output"
	"github.com/calemroelofs/enron-accounting-department/internal/service"
)

const txIDUsage = "Transaction ID"

func buildApp(cfg *config.Config, svc *service.Service) *cli.Command {
	return &cli.Command{
		Name: "finance-cli",
		Commands: []*cli.Command{
			{
				Name:   "sync",
				Usage:  "Sync bank transactions from Enable Banking fixture file",
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
		},
	}
}

func syncAction(svc *service.Service, cfg *config.Config) func(ctx context.Context, cmd *cli.Command) error {
	return func(_ context.Context, cmd *cli.Command) error {
		fixturePath := cmd.String("fixture")
		if fixturePath == "" {
			output.WriteError(os.Stdout, "sync requires --fixture path to Enable Banking JSON fixture")
			return nil
		}

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
			"status": "success",
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
