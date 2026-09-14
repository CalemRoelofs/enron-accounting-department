package service

import (
	"fmt"
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

const (
	ibanSpecificity = 1000
	ruleFieldIBAN   = "creditor_iban"
	ruleFieldTitle  = "title"
)

// RuleSpecificity returns a deterministic score for a rule.
// creditor_iban rules score 1000; title rules score the number of runes in pattern.
func RuleSpecificity(r CategoryRule) int {
	if r.Field == ruleFieldIBAN {
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
		case ruleFieldTitle:
			if title != "" && strings.Contains(strings.ToLower(title), strings.ToLower(r.Pattern)) {
				candidates = append(candidates, candidate{rule: r, priority: RuleSpecificity(r)})
			}
		case ruleFieldIBAN:
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

// AddRule inserts a new category rule and returns its ID.
//
//nolint:noctx // deliberate: DB.Exec uses background context implicitly
func (s *Service) AddRule(field, pattern, category, tags string) (int64, error) {
	if field != ruleFieldTitle && field != ruleFieldIBAN {
		return 0, fmt.Errorf("invalid field %q: must be 'title' or 'creditor_iban'", field)
	}
	if pattern == "" {
		return 0, fmt.Errorf("pattern must not be empty")
	}
	if category == "" {
		return 0, fmt.Errorf("category must not be empty")
	}

	if tags == "" {
		tags = "[]"
	}

	result, err := s.DB.Exec(
		"INSERT INTO category_rules (field, pattern, category, tags) VALUES (?, ?, ?, ?)",
		field, pattern, category, tags,
	)
	if err != nil {
		return 0, fmt.Errorf("inserting rule: %w", err)
	}

	return result.LastInsertId()
}

// ListRules returns all category rules ordered by id.
//
//nolint:noctx // deliberate: DB.Query uses background context implicitly
func (s *Service) ListRules() ([]CategoryRule, error) {
	rows, err := s.DB.Query("SELECT id, field, pattern, category, tags FROM category_rules ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("querying rules: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var rules []CategoryRule
	for rows.Next() {
		var r CategoryRule
		if scanErr := rows.Scan(&r.ID, &r.Field, &r.Pattern, &r.Category, &r.Tags); scanErr != nil {
			return nil, fmt.Errorf("scanning rule: %w", scanErr)
		}
		rules = append(rules, r)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterating rules: %w", iterErr)
	}

	return rules, nil
}

// RemoveRule deletes a category rule by ID.
//
//nolint:noctx // deliberate: DB.Exec uses background context implicitly
func (s *Service) RemoveRule(id int64) error {
	result, err := s.DB.Exec("DELETE FROM category_rules WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting rule: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking deletion: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("rule %d not found", id)
	}
	return nil
}

// ApplyRulesResult holds the result of ApplyRules.
type ApplyRulesResult struct {
	Status     string         `json:"status"`
	Scanned    int            `json:"scanned"`
	Matched    int            `json:"matched"`
	Updated    int            `json:"updated"`
	ByCategory map[string]int `json:"by_category"`
	DryRun     bool           `json:"dry_run"`
}

// ApplyRules recomputes categories for blank transactions using rules.
//
//nolint:noctx // deliberate: DB.Exec uses background context implicitly
func (s *Service) ApplyRules(dryRun bool) (*ApplyRulesResult, error) {
	rules, err := s.ListRules()
	if err != nil {
		return nil, err
	}

	result := &ApplyRulesResult{
		Status:     "success",
		ByCategory: make(map[string]int),
		DryRun:     dryRun,
	}

	rows, err := s.DB.Query(
		`SELECT id, transfer_title, creditor_iban FROM transactions WHERE category = '' ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("querying blank transactions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type blankRow struct {
		id           int64
		title        string
		creditorIBAN string
	}
	var blanks []blankRow
	for rows.Next() {
		var b blankRow
		var title, credIBAN *string
		if scanErr := rows.Scan(&b.id, &title, &credIBAN); scanErr != nil {
			return nil, fmt.Errorf("scanning transaction: %w", scanErr)
		}
		if title != nil {
			b.title = *title
		}
		if credIBAN != nil {
			b.creditorIBAN = *credIBAN
		}
		blanks = append(blanks, b)
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, fmt.Errorf("iterating transactions: %w", iterErr)
	}

	result.Scanned = len(blanks)

	var matched int
	for _, b := range blanks {
		matchedRule := MatchRule(rules, b.title, b.creditorIBAN)
		if matchedRule == nil {
			continue
		}
		matched++
		result.ByCategory[matchedRule.Category]++

		if dryRun {
			continue
		}

		_, updErr := s.DB.Exec(
			"UPDATE transactions SET category = ?, tags = ? WHERE id = ? AND category = ''",
			matchedRule.Category, matchedRule.Tags, b.id,
		)
		if updErr != nil {
			return nil, fmt.Errorf("updating transaction %d: %w", b.id, updErr)
		}
		result.Updated++
	}
	result.Matched = matched

	return result, nil
}
