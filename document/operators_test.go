package document_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

func TestBuiltinOperators_MatchCatalogSchemaReservedNames(t *testing.T) {
	var schema struct {
		Properties struct {
			Operators struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"operators"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(document.CatalogSchema(), &schema))

	var want []string
	for _, op := range document.BuiltinOperators {
		want = append(want, op, document.NegationPrefix+op)
	}
	sort.Strings(want)

	got := mapKeys(schema.Properties.Operators.Properties)
	sort.Strings(got)
	assert.Equal(t, want, got)
}

func TestBaseOperator(t *testing.T) {
	cases := map[string]struct {
		base    string
		negated bool
		ok      bool
	}{
		"eq":           {base: "eq", ok: true},
		"not_in_group": {base: "in_group", negated: true, ok: true},
		"not_exists":   {base: "exists", negated: true, ok: true},
		"is_vip":       {},
		"not_is_vip":   {},
		"not_":         {},
		"":             {},
	}
	for op, want := range cases {
		t.Run(op, func(t *testing.T) {
			base, negated, ok := document.BaseOperator(op)
			assert.Equal(t, want.base, base)
			assert.Equal(t, want.negated, negated)
			assert.Equal(t, want.ok, ok)
		})
	}
}
