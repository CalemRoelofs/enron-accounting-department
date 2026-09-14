package service_test

import (
	"math/rand"
	"testing"

	"github.com/calemroelofs/enron-accounting-department/internal/service"
)

func TestNormalizeIBAN(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"PL92 1160 2202 0000 0005 7581 0839", "PL92116022020000000575810839"},
		{"pl92116022020000000575810839", "PL92116022020000000575810839"},
		{"  pl 92 1160 2202 0000 0005 7581 0839  ", "PL92116022020000000575810839"},
		{"", ""},
	}
	for _, tt := range tests {
		got := service.NormalizeIBAN(tt.input)
		if got != tt.want {
			t.Errorf("NormalizeIBAN(%q): expected %q, got %q", tt.input, tt.want, got)
		}
	}
}

func TestRuleSpecificity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		rule service.CategoryRule
		want int
	}{
		{
			name: "creditor_iban scores 1000",
			rule: service.CategoryRule{Field: "creditor_iban", Pattern: "PL92116022020000000575810839"},
			want: 1000,
		},
		{
			name: "title scores by rune length",
			rule: service.CategoryRule{Field: "title", Pattern: "BIEDRONKA"},
			want: 9,
		},
		{
			name: "empty title scores 0",
			rule: service.CategoryRule{Field: "title", Pattern: ""},
			want: 0,
		},
		{
			name: "title unicode runes",
			rule: service.CategoryRule{Field: "title", Pattern: "ŻÓŁĆ"},
			want: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := service.RuleSpecificity(tt.rule)
			if got != tt.want {
				t.Errorf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestMatchRule_TitleSubstringCaseInsensitive(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 1, Field: "title", Pattern: "biedronka", Category: "Groceries", Tags: "[]"},
	}
	matched := service.MatchRule(rules, "JMP S.A. BIEDRONKA 3698  WROCLAW POL 2026-08-25", "")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.Category != "Groceries" {
		t.Errorf("expected Groceries, got %s", matched.Category)
	}
}

func TestMatchRule_CreditorIBANExactOnly(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 1, Field: "creditor_iban", Pattern: "PL92116022020000000575810839", Category: "Millennium", Tags: "[]"},
	}

	t.Run("exact match", func(t *testing.T) {
		t.Parallel()
		matched := service.MatchRule(rules, "", "PL92116022020000000575810839")
		if matched == nil {
			t.Fatal("expected a match for exact IBAN")
		}
	})

	t.Run("prefix does not match", func(t *testing.T) {
		t.Parallel()
		matched := service.MatchRule(rules, "", "PL9211602202000000057581083")
		if matched != nil {
			t.Fatal("expected no match for prefix IBAN")
		}
	})

	t.Run("suffix does not match", func(t *testing.T) {
		t.Parallel()
		matched := service.MatchRule(rules, "", "PL921160220200000005758108390")
		if matched != nil {
			t.Fatal("expected no match for extended IBAN")
		}
	})
}

func TestMatchRule_IBANNormalisation(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 1, Field: "creditor_iban", Pattern: "pl92 1160 2202 0000 0005 7581 0839",
			Category: "Millennium", Tags: "[]"},
	}

	t.Run("rule stored with spaces matches clean IBAN", func(t *testing.T) {
		t.Parallel()
		matched := service.MatchRule(rules, "", "PL92116022020000000575810839")
		if matched == nil {
			t.Fatal("expected match")
		}
	})

	t.Run("clean rule matches spaced IBAN", func(t *testing.T) {
		t.Parallel()
		rules2 := []service.CategoryRule{
			{ID: 1, Field: "creditor_iban", Pattern: "PL92116022020000000575810839",
				Category: "Millennium", Tags: "[]"},
		}
		matched := service.MatchRule(rules2, "", "PL92 1160 2202 0000 0005 7581 0839")
		if matched == nil {
			t.Fatal("expected match")
		}
	})
}

func TestMatchRule_NoMatchReturnsNil(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 1, Field: "title", Pattern: "BIEDRONKA", Category: "Groceries", Tags: "[]"},
		{ID: 2, Field: "creditor_iban", Pattern: "PL92116022020000000575810839", Category: "Millennium", Tags: "[]"},
	}
	matched := service.MatchRule(rules, "", "")
	if matched != nil {
		t.Fatal("expected nil when nothing matches")
	}
}

func TestMatchRule_PrefersLongerTitlePattern(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 1, Field: "title", Pattern: "LIDL", Category: "Groceries", Tags: "[]"},
		{ID: 2, Field: "title", Pattern: "SKLEP LIDL 1685", Category: "Lidl", Tags: `["weekly"]`},
	}
	matched := service.MatchRule(rules, "SKLEP LIDL 1685  WROCLAW POL 2026-08-25", "")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.ID != 2 {
		t.Errorf("expected rule id 2 (longer pattern) to win, got id %d", matched.ID)
	}
	if matched.Category != "Lidl" {
		t.Errorf("expected Lidl category, got %s", matched.Category)
	}
}

func TestMatchRule_IBANBeatsTitle(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 1, Field: "title", Pattern: "JMP S.A. BIEDRONKA 3698  WROCLAW POL 2026-08-25",
			Category: "GroceriesLong", Tags: "[]"},
		{ID: 2, Field: "creditor_iban", Pattern: "PL92116022020000000575810839", Category: "Millennium", Tags: "[]"},
	}
	matched := service.MatchRule(rules,
		"JMP S.A. BIEDRONKA 3698  WROCLAW POL 2026-08-25",
		"PL92116022020000000575810839")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.ID != 2 {
		t.Errorf("expected rule id 2 (IBAN, score 1000) to win, got id %d", matched.ID)
	}
}

func TestMatchRule_OnlyOneRuleApplied(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 1, Field: "title", Pattern: "BIEDRONKA", Category: "Groceries", Tags: "[]"},
		{ID: 2, Field: "title", Pattern: "BIEDRONKA", Category: "Duplicate", Tags: "[]"},
		{ID: 3, Field: "creditor_iban", Pattern: "PL92116022020000000575810839", Category: "Millennium", Tags: "[]"},
	}
	matched := service.MatchRule(rules, "BIEDRONKA", "PL92116022020000000575810839")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.Tags != "[]" {
		t.Errorf("expected Tags from winning rule, got %s", matched.Tags)
	}
}

func TestMatchRule_TieBreaksOnLowestID(t *testing.T) {
	t.Parallel()
	rules := []service.CategoryRule{
		{ID: 5, Field: "title", Pattern: "LIDL", Category: "Lidl_5", Tags: "[]"},
		{ID: 2, Field: "title", Pattern: "LIDL", Category: "Lidl_2", Tags: "[]"},
		{ID: 9, Field: "title", Pattern: "LIDL", Category: "Lidl_9", Tags: "[]"},
	}
	// Test with original order
	matched := service.MatchRule(rules, "LIDL SKLEP 1234", "")
	if matched == nil {
		t.Fatal("expected a match")
	}
	if matched.ID != 2 {
		t.Errorf("expected lowest id (2) to win, got id %d", matched.ID)
	}

	// Test with shuffled order (stability)
	shuffled := make([]service.CategoryRule, len(rules))
	copy(shuffled, rules)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	matched2 := service.MatchRule(shuffled, "LIDL SKLEP 1234", "")
	if matched2 == nil {
		t.Fatal("expected a match from shuffled input")
	}
	if matched2.ID != 2 {
		t.Errorf("expected lowest id (2) to win from shuffled input, got id %d", matched2.ID)
	}
}
