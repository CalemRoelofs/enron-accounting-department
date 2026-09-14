package service

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// CategoryRule defines an automatic categorisation rule.
type CategoryRule struct {
	ID       int64  `json:"id"`
	Field    string `json:"field"`
	Pattern  string `json:"pattern"`
	Category string `json:"category"`
	Tags     string `json:"tags"`
}

const ibanSpecificity = 1000

// RuleSpecificity returns a deterministic score for a rule.
// creditor_iban rules score 1000; title rules score the number of runes in pattern.
func RuleSpecificity(r CategoryRule) int {
	if r.Field == "creditor_iban" {
		return ibanSpecificity
	}
	return utf8.RuneCountInString(r.Pattern)
}

// NormalizeIBAN strips spaces and uppercases an IBAN string.
func NormalizeIBAN(iban string) string {
	return strings.ToUpper(strings.ReplaceAll(iban, " ", ""))
}

// MatchRule selects exactly one winning rule from the candidates that match.
// The winner has the highest specificity (ties broken by lowest ID).
// Returns nil if no rule matches.
func MatchRule(rules []CategoryRule, title, creditorIBAN string) *CategoryRule {
	type candidate struct {
		rule     CategoryRule
		priority int // higher = more specific, used as sort key
	}

	var candidates []candidate
	for _, r := range rules {
		switch r.Field {
		case "title":
			if title != "" && strings.Contains(strings.ToLower(title), strings.ToLower(r.Pattern)) {
				candidates = append(candidates, candidate{rule: r, priority: RuleSpecificity(r)})
			}
		case "creditor_iban":
			if creditorIBAN != "" && NormalizeIBAN(creditorIBAN) == NormalizeIBAN(r.Pattern) {
				candidates = append(candidates, candidate{rule: r, priority: RuleSpecificity(r)})
			}
		}
	}

	if len(candidates) == 0 {
		return nil
	}

	// Sort: highest priority first, then lowest ID
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority != candidates[j].priority {
			return candidates[i].priority > candidates[j].priority
		}
		return candidates[i].rule.ID < candidates[j].rule.ID
	})

	return &candidates[0].rule
}
