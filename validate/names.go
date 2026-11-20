package validate

import "regexp"

// Name rules, the same patterns as the JSON Schemas (a test keeps them in
// step).
const (
	identifierPattern = `^[a-z][a-z0-9_]{0,63}$`
	groupNamePattern  = `^[a-z0-9][a-z0-9-]{0,63}$`
	jiraPattern       = `^[A-Z][A-Z0-9]+-[0-9]+$`

	maxRuleNameLength = 100
	dateLayout        = "2006-01-02"
)

var (
	identifierRe = regexp.MustCompile(identifierPattern)
	groupNameRe  = regexp.MustCompile(groupNamePattern)
	jiraRe       = regexp.MustCompile(jiraPattern)
)
