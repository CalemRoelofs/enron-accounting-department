//nolint:govet // intentional err shadowing in Load and ValidateIBAN
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"strings"
)

const (
	digitBase   = 10
	ibanModulus = 97
	parseBase   = 10
)

// KnownCounterparty represents a known financial counterparty.
type KnownCounterparty struct {
	IBAN     string `json:"iban"`
	Label    string `json:"label"`
	Category string `json:"category"`
}

// Config holds application configuration.
type Config struct {
	EmployerIBAN         string              `json:"employer_iban"`
	SalaryMinGapDays     int                 `json:"salary_min_gap_days"`
	SalaryMinAmountCents *int64              `json:"salary_min_amount_cents,omitempty"`
	KnownCounterparties  []KnownCounterparty `json:"known_counterparties"`
}

// ConfigPath returns the path to the configuration file.
//
//nolint:gochecknoglobals // used in tests for path overrides
var ConfigPath = func() string {
	return os.ExpandEnv("${HOME}/.finance-cli/config.json")
}

// Load reads and parses the configuration file.
//
//nolint:govet // err variable shadowed across scopes intentionally
func Load() (*Config, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found at %s", path)
		}
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks the configuration for correctness.
func (c *Config) Validate() error {
	if c.EmployerIBAN == "" {
		return errors.New("employer_iban is required")
	}
	if err := ValidateIBAN(c.EmployerIBAN); err != nil {
		return fmt.Errorf("employer_iban: %w", err)
	}
	if c.SalaryMinGapDays < 0 {
		return errors.New("salary_min_gap_days must be non-negative")
	}
	if c.SalaryMinGapDays == 0 {
		c.SalaryMinGapDays = 25
	}

	seen := make(map[string]bool, len(c.KnownCounterparties))
	for i, cp := range c.KnownCounterparties {
		if cp.IBAN == "" {
			return fmt.Errorf("known_counterparties[%d].iban is required", i)
		}
		if err := ValidateIBAN(cp.IBAN); err != nil {
			return fmt.Errorf("known_counterparties[%d].iban: %w", i, err)
		}
		if cp.Label == "" {
			return fmt.Errorf("known_counterparties[%d].label is required", i)
		}
		if seen[cp.IBAN] {
			return fmt.Errorf("duplicate counterparty iban: %s", cp.IBAN)
		}
		seen[cp.IBAN] = true
	}
	return nil
}

// ValidateIBAN validates an IBAN string using the MOD-97 algorithm.
func ValidateIBAN(iban string) error {
	s := strings.ToUpper(strings.ReplaceAll(iban, " ", ""))
	if len(s) < 4 || len(s) > 34 {
		return fmt.Errorf("invalid IBAN length %d", len(s))
	}

	rearranged := s[4:] + s[:4]
	var numeric strings.Builder
	for _, c := range rearranged {
		switch {
		case c >= 'A' && c <= 'Z':
			fmt.Fprintf(&numeric, "%d", c-'A'+digitBase)
		case c >= '0' && c <= '9':
			numeric.WriteRune(c)
		default:
			return fmt.Errorf("invalid character %c in IBAN", c)
		}
	}

	n := new(big.Int)
	if _, ok := n.SetString(numeric.String(), parseBase); !ok {
		return errors.New("cannot parse IBAN as number")
	}
	mod := new(big.Int).Mod(n, big.NewInt(ibanModulus))
	if mod.Int64() != 1 {
		return errors.New("IBAN checksum failed (mod-97 != 1)")
	}
	return nil
}
