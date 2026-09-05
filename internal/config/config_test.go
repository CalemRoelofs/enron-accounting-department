//nolint:reassign // ConfigPath reassignment is intentional test pattern
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/calemroelofs/enron-accounting-department/internal/config"
)

func TestValidateIBAN_Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		iban string
	}{
		{"DE test", "DE89370400440532013000"},
		{"GB test", "GB33BUKB20201555555555"},
		{"AT test", "AT611904300234573201"},
		{"SK test", "SK3112000000198742637541"},
		{"PL test", "PL61109010140000071219812874"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := config.ValidateIBAN(tt.iban); err != nil {
				t.Errorf("unexpected error for %s: %v", tt.iban, err)
			}
		})
	}
}

func TestValidateIBAN_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		iban string
	}{
		{"too short", "PL12"},
		{"invalid chars", "PL12 3456 7890 ABCD!!"},
		{"bad checksum", "PL000000000000000000000000"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := config.ValidateIBAN(tt.iban); err == nil {
				t.Errorf("expected error for %s", tt.iban)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		cfg     config.Config
		wantErr bool
	}{
		{
			name: "valid minimal",
			cfg: config.Config{
				EmployerIBAN:     "DE89370400440532013000",
				SalaryMinGapDays: 25,
			},
			wantErr: false,
		},
		{
			name: "missing employer iban",
			cfg: config.Config{
				EmployerIBAN:     "",
				SalaryMinGapDays: 25,
			},
			wantErr: true,
		},
		{
			name: "negative gap days",
			cfg: config.Config{
				EmployerIBAN:     "DE89370400440532013000",
				SalaryMinGapDays: -1,
			},
			wantErr: true,
		},
		{
			name: "zero gap days defaults to 25",
			cfg: config.Config{
				EmployerIBAN:     "DE89370400440532013000",
				SalaryMinGapDays: 0,
			},
			wantErr: false,
		},
		{
			name: "duplicate counterparty ibans",
			cfg: config.Config{
				EmployerIBAN:     "DE89370400440532013000",
				SalaryMinGapDays: 25,
				KnownCounterparties: []config.KnownCounterparty{
					{IBAN: "GB33BUKB20201555555555", Label: "TR", Category: "Savings_TR"},
					{IBAN: "GB33BUKB20201555555555", Label: "TR dup", Category: "Savings_TR"},
				},
			},
			wantErr: true,
		},
		{
			name: "valid counterparties",
			cfg: config.Config{
				EmployerIBAN:     "DE89370400440532013000",
				SalaryMinGapDays: 25,
				KnownCounterparties: []config.KnownCounterparty{
					{IBAN: "GB33BUKB20201555555555", Label: "Trade Republic", Category: "Savings_TR"},
					{IBAN: "AT611904300234573201", Label: "IKE", Category: "Savings_IKE"},
				},
			},
			wantErr: false,
		},
		{
			name: "missing counterparty label",
			cfg: config.Config{
				EmployerIBAN:     "DE89370400440532013000",
				SalaryMinGapDays: 25,
				KnownCounterparties: []config.KnownCounterparty{
					{IBAN: "GB33BUKB20201555555555", Label: "", Category: "Savings_TR"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("expected error but got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

//nolint:paralleltest // Not parallel: shares config.ConfigPath with other TestLoad_* tests
func TestLoad_FileNotFound(t *testing.T) {
	orig := config.ConfigPath
	defer func() { config.ConfigPath = orig }()

	config.ConfigPath = func() string {
		return filepath.Join(t.TempDir(), "nonexistent.json")
	}

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for missing file")
	}
}

//nolint:paralleltest // Not parallel: shares config.ConfigPath with other TestLoad_* tests
func TestLoad_InvalidJSON(t *testing.T) {
	orig := config.ConfigPath
	defer func() { config.ConfigPath = orig }()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	config.ConfigPath = func() string { return path }

	if err := os.WriteFile(path, []byte("{invalid json}"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

//nolint:paralleltest // Not parallel: shares config.ConfigPath with other TestLoad_* tests
func TestLoad_Valid(t *testing.T) {
	orig := config.ConfigPath
	defer func() { config.ConfigPath = orig }()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	config.ConfigPath = func() string { return path }

	jsonData := `{
		"employer_iban": "DE89370400440532013000",
		"salary_min_gap_days": 25,
		"known_counterparties": [
			{"iban": "GB33BUKB20201555555555", "label": "Trade Republic", "category": "Savings_TR"}
		]
	}`

	if err := os.WriteFile(path, []byte(jsonData), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.EmployerIBAN != "DE89370400440532013000" {
		t.Errorf("expected employer IBAN, got %q", cfg.EmployerIBAN)
	}
	if cfg.SalaryMinGapDays != 25 {
		t.Errorf("expected 25 gap days, got %d", cfg.SalaryMinGapDays)
	}
	if len(cfg.KnownCounterparties) != 1 {
		t.Errorf("expected 1 counterparty, got %d", len(cfg.KnownCounterparties))
	}
}
