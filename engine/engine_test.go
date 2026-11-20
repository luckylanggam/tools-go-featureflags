package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/document"
	"github.com/luckylanggam/tools-go-featureflags/engine"
)

const testCatalog = `{
  "schema_version": 1,
  "attributes": {
    "network_id": {"type": "string"},
    "player_id":  {"type": "string"},
    "site_id":    {"type": "integer"},
    "stake":      {"type": "number"},
    "is_staff":   {"type": "boolean"}
  }
}`

const meta = `"owner": "marco", "jira": "ATOM-53", "remove_after": "2027-01-01"`

func parseCatalog(t *testing.T, data string) *document.Catalog {
	t.Helper()
	c, err := document.ParseCatalog([]byte(data))
	require.NoError(t, err)
	return c
}

// flagsDoc wraps flag bodies (name -> JSON without the metadata) and groups
// into a feature-flags document.
func flagsDoc(groups string, flags map[string]string) string {
	parts := make([]string, 0, len(flags))
	for name, body := range flags {
		parts = append(parts, fmt.Sprintf("%q: {%s, %s}", name, meta, body))
	}
	if groups == "" {
		groups = "{}"
	}
	return fmt.Sprintf(`{"schema_version": 1, "groups": %s, "flags": {%s}}`, groups, strings.Join(parts, ","))
}

func newEngine(t *testing.T, groups string, flags map[string]string) *engine.Engine {
	t.Helper()
	doc, err := document.ParseFlags([]byte(flagsDoc(groups, flags)))
	require.NoError(t, err)
	e, err := engine.New(doc, parseCatalog(t, testCatalog))
	require.NoError(t, err)
	return e
}

// oneCondition is a flag that is true only when cond holds.
func oneCondition(cond string) string {
	return fmt.Sprintf(`"enabled": true, "default": false, "rules": [{"name": "r", "if": [%s], "then": true}]`, cond)
}

func TestEvaluate_Order(t *testing.T) {
	e := newEngine(t, "", map[string]string{
		"killed": `"enabled": false, "default": true,
			"rules": [{"name": "everyone", "if": [], "then": true}]`,
		"first_match": `"enabled": true, "default": false, "rules": [
			{"name": "staff off", "if": [{"attr": "is_staff", "op": "eq", "value": true}], "then": false},
			{"name": "site 7", "if": [{"attr": "site_id", "op": "eq", "value": 7}], "then": true},
			{"name": "everyone", "if": [], "then": false}]`,
		"default_only": `"enabled": true, "default": true`,
		"and": `"enabled": true, "default": false, "rules": [
			{"name": "both", "if": [
				{"attr": "site_id", "op": "eq", "value": 7},
				{"attr": "network_id", "op": "eq", "value": "N-1"}], "then": true}]`,
		"rollout_then_rule": `"enabled": true, "default": false, "rules": [
			{"name": "nobody", "if": [], "then": true, "rollout": {"percentage": 0, "bucket_by": "player_id"}},
			{"name": "fallback", "if": [], "then": true}]`,
	})

	tests := []struct {
		name  string
		flag  string
		attrs engine.Attributes
		want  engine.Decision
	}{
		{
			name:  "kill switch wins over a matching rule and the default",
			flag:  "killed",
			attrs: engine.Attributes{"site_id": 7},
			want:  engine.Decision{Flag: "killed", Reason: engine.ReasonKillSwitch, RuleIndex: -1},
		},
		{
			name:  "first matching rule decides, even when it says false",
			flag:  "first_match",
			attrs: engine.Attributes{"is_staff": true, "site_id": 7},
			want:  engine.Decision{Flag: "first_match", Reason: engine.ReasonRule, Rule: "staff off", RuleIndex: 0},
		},
		{
			name:  "later rule matches when earlier ones do not",
			flag:  "first_match",
			attrs: engine.Attributes{"is_staff": false, "site_id": 7},
			want:  engine.Decision{Flag: "first_match", Enabled: true, Reason: engine.ReasonRule, Rule: "site 7", RuleIndex: 1},
		},
		{
			name:  "empty if matches everyone",
			flag:  "first_match",
			attrs: engine.Attributes{"is_staff": false, "site_id": 8},
			want:  engine.Decision{Flag: "first_match", Reason: engine.ReasonRule, Rule: "everyone", RuleIndex: 2},
		},
		{
			name: "no rules gives the default",
			flag: "default_only",
			want: engine.Decision{Flag: "default_only", Enabled: true, Reason: engine.ReasonDefault, RuleIndex: -1},
		},
		{
			name:  "all conditions must hold",
			flag:  "and",
			attrs: engine.Attributes{"site_id": 7, "network_id": "N-2"},
			want:  engine.Decision{Flag: "and", Reason: engine.ReasonDefault, RuleIndex: -1},
		},
		{
			name:  "all conditions hold",
			flag:  "and",
			attrs: engine.Attributes{"site_id": 7, "network_id": "N-1"},
			want:  engine.Decision{Flag: "and", Enabled: true, Reason: engine.ReasonRule, Rule: "both", RuleIndex: 0},
		},
		{
			name:  "caller outside the rollout falls through to the next rule",
			flag:  "rollout_then_rule",
			attrs: engine.Attributes{"player_id": "p-1"},
			want:  engine.Decision{Flag: "rollout_then_rule", Enabled: true, Reason: engine.ReasonRule, Rule: "fallback", RuleIndex: 1},
		},
		{
			name:  "missing attributes are reported with the decision",
			flag:  "first_match",
			attrs: engine.Attributes{},
			want: engine.Decision{Flag: "first_match", Reason: engine.ReasonRule, Rule: "everyone", RuleIndex: 2,
				MissingAttrs: []string{"is_staff", "site_id"}},
		},
		{
			name:  "invalid attributes are reported with the decision",
			flag:  "and",
			attrs: engine.Attributes{"site_id": "seven"},
			want: engine.Decision{Flag: "and", Reason: engine.ReasonDefault, RuleIndex: -1,
				InvalidAttrs: []string{"site_id"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.Evaluate(context.Background(), tt.flag, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)

			enabled, err := e.IsEnabled(context.Background(), tt.flag, tt.attrs)
			require.NoError(t, err)
			assert.Equal(t, tt.want.Enabled, enabled)
		})
	}
}

func TestEvaluate_UnknownFlag(t *testing.T) {
	e := newEngine(t, "", map[string]string{"known": `"enabled": true, "default": true`})

	got, err := e.Evaluate(context.Background(), "unknown", engine.Attributes{"site_id": 1})
	require.EqualError(t, err, "feature flag 'unknown' does not exist")
	assert.Equal(t, engine.Decision{Flag: "unknown", Reason: engine.ReasonUnknownFlag, RuleIndex: -1}, got)

	enabled, err := e.IsEnabled(context.Background(), "unknown", nil)
	require.Error(t, err)
	assert.False(t, enabled)
}

func TestEvaluate_Operators(t *testing.T) {
	groups := `{
		"staff":  {"attr": "player_id", "values": ["p-1", "p-2"]},
		"sites":  {"attr": "site_id", "values": [1, 2]},
		"nets":   {"attr": "network_id", "values": [101, "N-1"]}
	}`
	type check struct {
		attrs engine.Attributes
		want  bool
	}
	tests := []struct {
		name   string
		cond   string
		checks []check
	}{
		{"eq string", `{"attr": "network_id", "op": "eq", "value": "N-1"}`, []check{
			{engine.Attributes{"network_id": "N-1"}, true},
			{engine.Attributes{"network_id": "N-2"}, false},
			{engine.Attributes{}, false},
		}},
		{"not_eq string", `{"attr": "network_id", "op": "not_eq", "value": "N-1"}`, []check{
			{engine.Attributes{"network_id": "N-2"}, true},
			{engine.Attributes{"network_id": "N-1"}, false},
			{engine.Attributes{}, false},
			{engine.Attributes{"network_id": nil}, false},
		}},
		{"eq integer", `{"attr": "site_id", "op": "eq", "value": 7}`, []check{
			{engine.Attributes{"site_id": 7}, true},
			{engine.Attributes{"site_id": int64(7)}, true},
			{engine.Attributes{"site_id": "7"}, true},
			{engine.Attributes{"site_id": 7.0}, true},
			{engine.Attributes{"site_id": 8}, false},
			{engine.Attributes{"site_id": 7.5}, false},
		}},
		{"eq integer above 2^53", `{"attr": "site_id", "op": "eq", "value": 9007199254740993.0}`, []check{
			{engine.Attributes{"site_id": json.Number("9007199254740993")}, true},
			{engine.Attributes{"site_id": "9007199254740993"}, true},
			{engine.Attributes{"site_id": int64(9007199254740992)}, false},
			{engine.Attributes{"site_id": json.Number("9007199254740992.0")}, false},
		}},
		{"eq boolean", `{"attr": "is_staff", "op": "eq", "value": true}`, []check{
			{engine.Attributes{"is_staff": true}, true},
			{engine.Attributes{"is_staff": false}, false},
			{engine.Attributes{"is_staff": "true"}, false},
		}},
		{"in", `{"attr": "player_id", "op": "in", "value": ["p-1", "p-2"]}`, []check{
			{engine.Attributes{"player_id": "p-2"}, true},
			{engine.Attributes{"player_id": "p-3"}, false},
			{engine.Attributes{}, false},
		}},
		{"not_in", `{"attr": "site_id", "op": "not_in", "value": [1, 2]}`, []check{
			{engine.Attributes{"site_id": 3}, true},
			{engine.Attributes{"site_id": 2}, false},
			{engine.Attributes{}, false},
		}},
		{"in_group", `{"attr": "player_id", "op": "in_group", "value": "staff"}`, []check{
			{engine.Attributes{"player_id": "p-1"}, true},
			{engine.Attributes{"player_id": "p-9"}, false},
			{engine.Attributes{}, false},
		}},
		{"not_in_group", `{"attr": "site_id", "op": "not_in_group", "value": "sites"}`, []check{
			{engine.Attributes{"site_id": 3}, true},
			{engine.Attributes{"site_id": "1"}, false},
			{engine.Attributes{}, false},
		}},
		{"in_group with numbers on a string attribute", `{"attr": "network_id", "op": "in_group", "value": "nets"}`, []check{
			{engine.Attributes{"network_id": 101}, true},
			{engine.Attributes{"network_id": "101"}, true},
			{engine.Attributes{"network_id": "N-1"}, true},
			{engine.Attributes{"network_id": 102}, false},
		}},
		{"gt integer", `{"attr": "site_id", "op": "gt", "value": 10}`, []check{
			{engine.Attributes{"site_id": 11}, true},
			{engine.Attributes{"site_id": 10}, false},
			{engine.Attributes{"site_id": 9}, false},
		}},
		{"not_gt integer", `{"attr": "site_id", "op": "not_gt", "value": 10}`, []check{
			{engine.Attributes{"site_id": 10}, true},
			{engine.Attributes{"site_id": 11}, false},
			{engine.Attributes{}, false},
		}},
		{"lt number", `{"attr": "stake", "op": "lt", "value": 2.5}`, []check{
			{engine.Attributes{"stake": 2.49}, true},
			{engine.Attributes{"stake": 2}, true},
			{engine.Attributes{"stake": json.Number("2.5")}, false},
			{engine.Attributes{"stake": "3"}, false},
		}},
		{"not_lt number", `{"attr": "stake", "op": "not_lt", "value": 2.5}`, []check{
			{engine.Attributes{"stake": 2.5}, true},
			{engine.Attributes{"stake": 1}, false},
			{engine.Attributes{}, false},
		}},
		{"starts_with", `{"attr": "player_id", "op": "starts_with", "value": "test-"}`, []check{
			{engine.Attributes{"player_id": "test-1"}, true},
			{engine.Attributes{"player_id": "p-1"}, false},
		}},
		{"not_starts_with", `{"attr": "player_id", "op": "not_starts_with", "value": "test-"}`, []check{
			{engine.Attributes{"player_id": "p-1"}, true},
			{engine.Attributes{"player_id": "test-1"}, false},
			{engine.Attributes{}, false},
		}},
		{"contains", `{"attr": "player_id", "op": "contains", "value": "qa"}`, []check{
			{engine.Attributes{"player_id": "p-qa-1"}, true},
			{engine.Attributes{"player_id": "p-1"}, false},
		}},
		{"not_contains", `{"attr": "player_id", "op": "not_contains", "value": "qa"}`, []check{
			{engine.Attributes{"player_id": "p-1"}, true},
			{engine.Attributes{"player_id": "p-qa-1"}, false},
			{engine.Attributes{}, false},
		}},
		{"matches is unanchored", `{"attr": "player_id", "op": "matches", "value": "qa-[0-9]+"}`, []check{
			{engine.Attributes{"player_id": "p-qa-12"}, true},
			{engine.Attributes{"player_id": "qa-x"}, false},
		}},
		{"matches anchored by the author", `{"attr": "player_id", "op": "matches", "value": "^qa-[0-9]+$"}`, []check{
			{engine.Attributes{"player_id": "qa-12"}, true},
			{engine.Attributes{"player_id": "p-qa-12"}, false},
		}},
		{"not_matches", `{"attr": "player_id", "op": "not_matches", "value": "^qa-"}`, []check{
			{engine.Attributes{"player_id": "p-1"}, true},
			{engine.Attributes{"player_id": "qa-1"}, false},
			{engine.Attributes{}, false},
		}},
		{"exists", `{"attr": "player_id", "op": "exists"}`, []check{
			{engine.Attributes{"player_id": "p-1"}, true},
			{engine.Attributes{"player_id": 42}, true},
			{engine.Attributes{"player_id": nil}, false},
			{engine.Attributes{}, false},
		}},
		{"not_exists", `{"attr": "player_id", "op": "not_exists"}`, []check{
			{engine.Attributes{}, true},
			{engine.Attributes{"player_id": nil}, true},
			{engine.Attributes{"player_id": "p-1"}, false},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEngine(t, groups, map[string]string{"f": oneCondition(tt.cond)})
			for _, c := range tt.checks {
				got, err := e.IsEnabled(context.Background(), "f", c.attrs)
				require.NoError(t, err)
				assert.Equal(t, c.want, got, "attrs %v", c.attrs)
			}
		})
	}
}

func TestEvaluate_TypeNormalisation(t *testing.T) {
	e := newEngine(t, "", map[string]string{
		"by_string":  oneCondition(`{"attr": "network_id", "op": "eq", "value": "101"}`),
		"by_integer": oneCondition(`{"attr": "site_id", "op": "eq", "value": 101}`),
		"by_number":  oneCondition(`{"attr": "stake", "op": "eq", "value": 1.5}`),
		"by_boolean": oneCondition(`{"attr": "is_staff", "op": "eq", "value": false}`),
	})
	tests := []struct {
		flag    string
		value   any
		want    bool
		invalid bool
	}{
		{"by_string", "101", true, false},
		{"by_string", 101, true, false},
		{"by_string", int64(101), true, false},
		{"by_string", uint8(101), true, false},
		{"by_string", 101.0, true, false},
		{"by_string", json.Number("101"), true, false},
		{"by_string", 101.5, false, true},
		{"by_string", true, false, true},
		{"by_string", []string{"101"}, false, true},

		{"by_integer", 101, true, false},
		{"by_integer", int32(101), true, false},
		{"by_integer", uint64(101), true, false},
		{"by_integer", float32(101), true, false},
		{"by_integer", "101", true, false},
		{"by_integer", json.Number("101"), true, false},
		{"by_integer", json.Number("1.01e2"), true, false},
		{"by_integer", "101.0", false, true},
		{"by_integer", " 101", false, true},
		{"by_integer", 101.5, false, true},
		{"by_integer", uint64(1 << 63), false, true},
		{"by_integer", 1e19, false, true},
		{"by_integer", false, false, true},

		{"by_number", 1.5, true, false},
		{"by_number", float32(1.5), true, false},
		{"by_number", "1.5", true, false},
		{"by_number", json.Number("1.5"), true, false},
		{"by_number", 2, false, false},
		{"by_number", uint64(1 << 63), false, false},
		{"by_number", uint(math.MaxUint), false, false},
		{"by_number", "NaN", false, true},
		{"by_number", "abc", false, true},
		{"by_number", true, false, true},

		{"by_boolean", false, true, false},
		{"by_boolean", true, false, false},
		{"by_boolean", "false", false, true},
		{"by_boolean", 0, false, true},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/%T(%v)", tt.flag, tt.value, tt.value), func(t *testing.T) {
			attr := map[string]string{"by_string": "network_id", "by_integer": "site_id", "by_number": "stake", "by_boolean": "is_staff"}[tt.flag]
			got, err := e.Evaluate(context.Background(), tt.flag, engine.Attributes{attr: tt.value})
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Enabled)
			if tt.invalid {
				assert.Equal(t, []string{attr}, got.InvalidAttrs)
			} else {
				assert.Empty(t, got.InvalidAttrs)
			}
		})
	}
}

func TestEvaluate_ExistsReportsMissingAttribute(t *testing.T) {
	e := newEngine(t, "", map[string]string{
		"exists":     oneCondition(`{"attr": "player_id", "op": "exists"}`),
		"not_exists": oneCondition(`{"attr": "player_id", "op": "not_exists"}`),
	})

	got, err := e.Evaluate(context.Background(), "exists", engine.Attributes{})
	require.NoError(t, err)
	assert.Equal(t, engine.Decision{Flag: "exists", Reason: engine.ReasonDefault, RuleIndex: -1, MissingAttrs: []string{"player_id"}}, got)

	got, err = e.Evaluate(context.Background(), "not_exists", engine.Attributes{"player_id": "p-1"})
	require.NoError(t, err)
	assert.Equal(t, engine.Decision{Flag: "not_exists", Reason: engine.ReasonDefault, RuleIndex: -1}, got)
}

func TestEvaluate_RolloutMissingBucketAttribute(t *testing.T) {
	e := newEngine(t, "", map[string]string{
		"f": `"enabled": true, "default": false, "rules": [
			{"name": "all", "if": [], "then": true, "rollout": {"percentage": 100, "bucket_by": "player_id"}}]`,
	})

	got, err := e.Evaluate(context.Background(), "f", engine.Attributes{"site_id": 1})
	require.NoError(t, err)
	assert.Equal(t, engine.Decision{Flag: "f", Reason: engine.ReasonDefault, RuleIndex: -1, MissingAttrs: []string{"player_id"}}, got)

	got, err = e.Evaluate(context.Background(), "f", engine.Attributes{"player_id": "p-1"})
	require.NoError(t, err)
	assert.True(t, got.Enabled, "100%% includes every caller that sent the attribute")
}

func TestEvaluate_RolloutBounds(t *testing.T) {
	rolloutFlag := func(pct int) string {
		return fmt.Sprintf(`"enabled": true, "default": false, "rules": [
			{"name": "r", "if": [], "then": true, "rollout": {"percentage": %d, "bucket_by": "site_id"}}]`, pct)
	}
	e := newEngine(t, "", map[string]string{"none": rolloutFlag(0), "half": rolloutFlag(50), "all": rolloutFlag(100)})

	counts := map[string]int{}
	for id := 0; id < 1000; id++ {
		for _, f := range []string{"none", "half", "all"} {
			on, err := e.IsEnabled(context.Background(), f, engine.Attributes{"site_id": id})
			require.NoError(t, err)
			if on {
				counts[f]++
			}
		}
	}
	assert.Equal(t, 0, counts["none"], "0% includes nobody")
	assert.InDelta(t, 500, counts["half"], 60)
	assert.Equal(t, 1000, counts["all"], "100% includes everyone")
}

func TestNew_Errors(t *testing.T) {
	tests := []struct {
		name    string
		groups  string
		body    string
		wantErr string
	}{
		{"attribute not in catalog", "", oneCondition(`{"attr": "tenant_id", "op": "eq", "value": "x"}`),
			`flag "f" rule 0 "r": if[0]: attribute "tenant_id" is not in the catalog`},
		{"operator not built in or declared", "", oneCondition(`{"attr": "player_id", "op": "is_vip", "value": true}`),
			`flag "f" rule 0 "r": if[0]: operator "is_vip" is not built in or declared in the catalog`},
		{"negated operator not built in or declared", "", oneCondition(`{"attr": "player_id", "op": "not_is_vip", "value": true}`),
			`operator "not_is_vip" is not built in or declared in the catalog`},
		{"unknown group", "", oneCondition(`{"attr": "player_id", "op": "in_group", "value": "nope"}`),
			`operator "in_group": group "nope" does not exist`},
		{"group over another attribute", `{"g": {"attr": "network_id", "values": ["N-1"]}}`,
			oneCondition(`{"attr": "player_id", "op": "in_group", "value": "g"}`),
			`group "g" is over attribute "network_id", not "player_id"`},
		{"group value of the wrong type", `{"g": {"attr": "site_id", "values": ["one"]}}`,
			oneCondition(`{"attr": "site_id", "op": "in_group", "value": "g"}`),
			`group "g": value one is not a valid integer`},
		{"eq value of the wrong type", "", oneCondition(`{"attr": "site_id", "op": "eq", "value": 1.5}`),
			`operator "eq": value 1.5 is not a valid integer`},
		{"gt on a string attribute", "", oneCondition(`{"attr": "player_id", "op": "gt", "value": "a"}`),
			`attribute "player_id" is string, the operator needs integer or number`},
		{"starts_with on an integer attribute", "", oneCondition(`{"attr": "site_id", "op": "starts_with", "value": "1"}`),
			`attribute "site_id" is integer, the operator needs string`},
		{"matches on a boolean attribute", "", oneCondition(`{"attr": "is_staff", "op": "matches", "value": "t"}`),
			`attribute "is_staff" is boolean, the operator needs string`},
		{"bad regular expression", "", oneCondition(`{"attr": "player_id", "op": "matches", "value": "("}`),
			`invalid regular expression "("`},
		{"rollout on an unknown attribute", "", `"enabled": true, "default": false, "rules": [
			{"name": "r", "if": [], "then": true, "rollout": {"percentage": 10, "bucket_by": "tenant_id"}}]`,
			`flag "f" rule 0 "r": rollout: attribute "tenant_id" is not in the catalog`},
		{"rollout over 100", "", `"enabled": true, "default": false, "rules": [
			{"name": "r", "if": [], "then": true, "rollout": {"percentage": 101, "bucket_by": "player_id"}}]`,
			`rollout: percentage 101 is outside 0..100`},
		{"in with a single value", "", oneCondition(`{"attr": "player_id", "op": "in", "value": "p-1"}`),
			`operator "in": value must be a list`},
		{"eq with a list", "", oneCondition(`{"attr": "player_id", "op": "eq", "value": ["p-1"]}`),
			`operator "eq": value must be a single value`},
		{"eq without a value", "", oneCondition(`{"attr": "player_id", "op": "eq"}`),
			`operator "eq": value must be a value`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := document.ParseFlags([]byte(flagsDoc(tt.groups, map[string]string{"f": tt.body})))
			require.NoError(t, err)
			e, err := engine.New(doc, parseCatalog(t, testCatalog))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, e)
		})
	}
}

func TestNew_NilDocuments(t *testing.T) {
	_, err := engine.New(nil, parseCatalog(t, testCatalog))
	assert.EqualError(t, err, "engine: feature-flags document is nil")

	_, err = engine.New(&document.Flags{SchemaVersion: 1}, nil)
	assert.EqualError(t, err, "engine: catalog document is nil")
}

// The format's valid fixtures compile, except full.json: it checks shape
// only, so it uses the custom operator is_vip and gt on a string attribute,
// which the engine (and Validate) reject.
func TestNew_DocumentFixtures(t *testing.T) {
	catalog := parseCatalog(t, testCatalog)
	paths, err := filepath.Glob("../document/testdata/flags/valid/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			data, err := os.ReadFile(p)
			require.NoError(t, err)
			doc, err := document.ParseFlags(data)
			require.NoError(t, err)

			_, err = engine.New(doc, catalog)
			if filepath.Base(p) == "full.json" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestEvaluate_TDDExample(t *testing.T) {
	data, err := os.ReadFile("../document/testdata/flags/valid/tdd-example.json")
	require.NoError(t, err)
	doc, err := document.ParseFlags(data)
	require.NoError(t, err)
	e, err := engine.New(doc, parseCatalog(t, testCatalog))
	require.NoError(t, err)

	got, err := e.Evaluate(context.Background(), "new_withdraw_flow", engine.Attributes{"network_id": "N-1"})
	require.NoError(t, err)
	assert.Equal(t, "canary", got.Rule)
	assert.True(t, got.Enabled)

	got, err = e.Evaluate(context.Background(), "new_withdraw_flow", engine.Attributes{})
	require.NoError(t, err)
	assert.Equal(t, engine.Decision{Flag: "new_withdraw_flow", Reason: engine.ReasonDefault, RuleIndex: -1,
		MissingAttrs: []string{"network_id"}}, got)
}

func BenchmarkEvaluate(b *testing.B) {
	doc, err := document.ParseFlags([]byte(flagsDoc(`{"staff": {"attr": "player_id", "values": ["p-1", "p-2"]}}`, map[string]string{
		"f": `"enabled": true, "default": false, "rules": [
			{"name": "staff", "if": [{"attr": "player_id", "op": "in_group", "value": "staff"}], "then": true},
			{"name": "site", "if": [{"attr": "site_id", "op": "gt", "value": 100}], "then": true},
			{"name": "10%", "if": [], "then": true, "rollout": {"percentage": 10, "bucket_by": "player_id"}}]`,
	})))
	require.NoError(b, err)
	catalog, err := document.ParseCatalog([]byte(testCatalog))
	require.NoError(b, err)
	e, err := engine.New(doc, catalog)
	require.NoError(b, err)
	attrs := engine.Attributes{"player_id": "p-123456", "site_id": 7}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = e.Evaluate(ctx, "f", attrs)
	}
}
