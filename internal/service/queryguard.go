package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

func GuardSQL(sqlStr string) error {
	dangerousKeywords := []string{
		"ATTACH", "PRAGMA", "VACUUM",
		"ALTER", "DROP", "CREATE", "INSERT", "UPDATE", "DELETE",
		"REINDEX", "REPLACE", "ANALYZE", "DETACH",
	}

	sqlStr = strings.TrimSpace(sqlStr)
	if sqlStr == "" {
		return errors.New("empty query")
	}

	upper := strings.ToUpper(sqlStr)
	if !strings.HasPrefix(upper, "SELECT") {
		return errors.New("only SELECT queries are allowed")
	}

	if strings.ContainsRune(sqlStr, ';') {
		return errors.New("multiple statements are not allowed (semicolon detected)")
	}

	for _, kw := range dangerousKeywords {
		re := regexp.MustCompile(`\b` + kw + `\b`)
		if re.MatchString(upper) {
			return fmt.Errorf("keyword %q is not allowed in query mode", strings.ToLower(kw))
		}
	}

	return nil
}
