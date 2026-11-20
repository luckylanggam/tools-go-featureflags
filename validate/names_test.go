package validate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

// The JSON Schemas and Validate must agree on names, or a document could
// pass one and fail the other.
func TestNamePatterns_MatchTheSchemas(t *testing.T) {
	var flags struct {
		Properties struct {
			Groups struct {
				PatternProperties map[string]json.RawMessage `json:"patternProperties"`
			} `json:"groups"`
			Flags struct {
				PatternProperties map[string]json.RawMessage `json:"patternProperties"`
			} `json:"flags"`
		} `json:"properties"`
		Definitions struct {
			Attr struct {
				Pattern string `json:"pattern"`
			} `json:"attr"`
			Flag struct {
				Properties struct {
					Jira struct {
						Pattern string `json:"pattern"`
					} `json:"jira"`
				} `json:"properties"`
			} `json:"flag"`
			Rule struct {
				Properties struct {
					Name struct {
						MaxLength int `json:"maxLength"`
					} `json:"name"`
				} `json:"properties"`
			} `json:"rule"`
			Group struct {
				Properties struct {
					Values struct {
						MaxItems int `json:"maxItems"`
					} `json:"values"`
				} `json:"properties"`
			} `json:"group"`
		} `json:"definitions"`
	}
	require.NoError(t, json.Unmarshal(document.FlagsSchema(), &flags))

	assert.Contains(t, flags.Properties.Flags.PatternProperties, identifierPattern)
	assert.Contains(t, flags.Properties.Groups.PatternProperties, groupNamePattern)
	assert.Equal(t, identifierPattern, flags.Definitions.Attr.Pattern)
	assert.Equal(t, jiraPattern, flags.Definitions.Flag.Properties.Jira.Pattern)
	assert.Equal(t, maxRuleNameLength, flags.Definitions.Rule.Properties.Name.MaxLength)
	assert.Equal(t, MaxGroupValues, flags.Definitions.Group.Properties.Values.MaxItems)

	var catalog struct {
		Properties map[string]struct {
			PatternProperties map[string]json.RawMessage `json:"patternProperties"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(document.CatalogSchema(), &catalog))
	for _, section := range []string{"attributes", "operators", "resolvers"} {
		assert.Contains(t, catalog.Properties[section].PatternProperties, identifierPattern, section)
	}
}
