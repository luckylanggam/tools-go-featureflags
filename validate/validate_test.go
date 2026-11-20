package validate_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/document"
	"github.com/luckylanggam/tools-go-featureflags/validate"
)

const catalogJSON = `{
  "schema_version": 1,
  "attributes": {
    "network_id": { "type": "string" },
    "player_id": { "type": "string" },
    "age_days": { "type": "integer" },
    "stake": { "type": "number" },
    "is_staff": { "type": "boolean" }
  },
  "operators": { "is_vip": { "value_type": "boolean" } },
  "resolvers": { "network_id": { "requires": ["player_id"] } }
}`

func parseCatalog(t *testing.T, data string) *document.Catalog {
	t.Helper()
	c, err := document.ParseCatalog([]byte(data))
	require.NoError(t, err)
	return c
}

// flagsDoc builds a document with the given groups and one flag "f" with
// the given rules (both JSON fragments).
func flagsDoc(t *testing.T, groups, rules string) *document.Flags {
	t.Helper()
	if groups == "" {
		groups = "{}"
	}
	if rules == "" {
		rules = "[]"
	}
	data := fmt.Sprintf(`{
  "schema_version": 1,
  "groups": %s,
  "flags": {
    "f": {
      "owner": "ivo", "jira": "ATOM-54", "remove_after": "2027-03-01",
      "enabled": true, "rules": %s, "default": false
    }
  }
}`, groups, rules)
	f, err := document.ParseFlags([]byte(data))
	require.NoError(t, err)
	return f
}

func rule(conditions string) string {
	return `[{"name": "r", "if": [` + conditions + `], "then": true}]`
}

func values(n int) string {
	vs := make([]string, n)
	for i := range vs {
		vs[i] = fmt.Sprintf(`"N-%d"`, i)
	}
	return "[" + strings.Join(vs, ",") + "]"
}

func problems(t *testing.T, err error) []validate.Problem {
	t.Helper()
	var verr *validate.Error
	require.ErrorAs(t, err, &verr)
	return verr.Problems
}

const condPath = `flags.f.rules[0] "r".if[0]`

func TestValidate_Valid(t *testing.T) {
	groups := `{
  "canary-1": { "attr": "network_id", "values": ["N-1", "N-2"] },
  "old-players": { "attr": "age_days", "values": [365, 730.0, 1e3] }
}`
	rules := `[
  { "name": "canary", "if": [{ "attr": "network_id", "op": "in_group", "value": "canary-1" }], "then": true },
  { "name": "not canary", "if": [{ "attr": "network_id", "op": "not_in_group", "value": "canary-1" },
                                  { "attr": "age_days", "op": "in_group", "value": "old-players" }], "then": false },
  { "name": "every operator", "if": [
      { "attr": "player_id", "op": "eq", "value": "P-1" },
      { "attr": "player_id", "op": "not_eq", "value": "P-2" },
      { "attr": "player_id", "op": "in", "value": ["P-1", "P-3"] },
      { "attr": "age_days", "op": "gt", "value": 30 },
      { "attr": "age_days", "op": "not_lt", "value": 1.0 },
      { "attr": "stake", "op": "lt", "value": 10.5 },
      { "attr": "stake", "op": "gt", "value": 2 },
      { "attr": "player_id", "op": "starts_with", "value": "P-" },
      { "attr": "player_id", "op": "not_contains", "value": "test" },
      { "attr": "player_id", "op": "matches", "value": "^P-[0-9]+$" },
      { "attr": "is_staff", "op": "eq", "value": true },
      { "attr": "is_staff", "op": "exists" },
      { "attr": "network_id", "op": "not_exists" },
      { "attr": "player_id", "op": "is_vip", "value": true },
      { "attr": "player_id", "op": "not_is_vip", "value": false }
    ], "then": true },
  { "name": "10% rollout", "if": [], "then": true, "rollout": { "percentage": 10, "bucket_by": "network_id" } }
]`
	warnings, err := validate.Validate(flagsDoc(t, groups, rules), parseCatalog(t, catalogJSON))
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestValidate_Rejects(t *testing.T) {
	cases := map[string]struct {
		groups, rules string
		want          validate.Problem
	}{
		// Groups.
		"unknown group": {
			rules: rule(`{"attr": "network_id", "op": "in_group", "value": "nope"}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `group "nope" does not exist`},
		},
		"group over another attribute": {
			groups: `{"staff": {"attr": "player_id", "values": ["P-1"]}}`,
			rules:  rule(`{"attr": "network_id", "op": "not_in_group", "value": "staff"}`),
			want:   validate.Problem{Path: condPath + ".value", Message: `group "staff" is over attribute "player_id", not "network_id"`},
		},
		"group name not a string": {
			rules: rule(`{"attr": "network_id", "op": "in_group", "value": 1}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `operator "in_group" needs a group name`},
		},
		"group over 200 values": {
			groups: `{"big": {"attr": "network_id", "values": ` + values(201) + `}}`,
			want:   validate.Problem{Path: "groups.big.values", Message: "group has 201 values, want 1 to 200"},
		},
		"empty group": {
			groups: `{"empty": {"attr": "network_id", "values": []}}`,
			want:   validate.Problem{Path: "groups.empty.values", Message: "group has 0 values, want 1 to 200"},
		},
		"group attribute not in catalog": {
			groups: `{"g": {"attr": "plan", "values": ["gold"]}}`,
			want:   validate.Problem{Path: "groups.g.attr", Message: `attribute "plan" is not in the catalog`},
		},
		"group value of the wrong type": {
			groups: `{"g": {"attr": "age_days", "values": [1, "2"]}}`,
			want:   validate.Problem{Path: "groups.g.values[1]", Message: `value "2" is not a valid integer`},
		},
		"group value listed twice": {
			groups: `{"g": {"attr": "age_days", "values": [1, 1.0]}}`,
			want:   validate.Problem{Path: "groups.g.values[1]", Message: "value 1.0 is listed twice"},
		},
		"bad group name": {
			groups: `{"Canary_1": {"attr": "network_id", "values": ["N-1"]}}`,
			want:   validate.Problem{Path: "groups.Canary_1", Message: `group name "Canary_1" must match ^[a-z0-9][a-z0-9-]{0,63}$`},
		},

		// Catalog references.
		"attribute not in catalog": {
			rules: rule(`{"attr": "plan", "op": "eq", "value": "gold"}`),
			want:  validate.Problem{Path: condPath + ".attr", Message: `attribute "plan" is not in the catalog`},
		},
		"operator not in catalog": {
			rules: rule(`{"attr": "player_id", "op": "is_gold", "value": true}`),
			want:  validate.Problem{Path: condPath + ".op", Message: `operator "is_gold" is neither built-in nor in the catalog`},
		},
		"rollout bucket attribute not in catalog": {
			rules: `[{"name": "r", "if": [], "then": true, "rollout": {"percentage": 10, "bucket_by": "site_id"}}]`,
			want:  validate.Problem{Path: `flags.f.rules[0] "r".rollout.bucket_by`, Message: `attribute "site_id" is not in the catalog`},
		},

		// Types.
		"string for an integer": {
			rules: rule(`{"attr": "age_days", "op": "eq", "value": "5"}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `value "5" is not a valid integer`},
		},
		"fraction for an integer": {
			rules: rule(`{"attr": "age_days", "op": "gt", "value": 1.5}`),
			want:  validate.Problem{Path: condPath + ".value", Message: "value 1.5 is not a valid integer"},
		},
		"number for a string": {
			rules: rule(`{"attr": "network_id", "op": "eq", "value": 101}`),
			want:  validate.Problem{Path: condPath + ".value", Message: "value 101 is not a valid string"},
		},
		"wrong type in a list": {
			rules: rule(`{"attr": "player_id", "op": "in", "value": ["P-1", true]}`),
			want:  validate.Problem{Path: condPath + ".value[1]", Message: "value true is not a valid string"},
		},
		"custom operator value type": {
			rules: rule(`{"attr": "player_id", "op": "is_vip", "value": "yes"}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `value "yes" is not a valid boolean`},
		},
		"negated custom operator value type": {
			rules: rule(`{"attr": "player_id", "op": "not_is_vip", "value": "yes"}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `value "yes" is not a valid boolean`},
		},
		"negated unknown custom operator": {
			rules: rule(`{"attr": "player_id", "op": "not_is_gold", "value": true}`),
			want:  validate.Problem{Path: condPath + ".op", Message: `operator "not_is_gold" is neither built-in nor in the catalog`},
		},
		"gt on a string attribute": {
			rules: rule(`{"attr": "network_id", "op": "gt", "value": "N-1"}`),
			want:  validate.Problem{Path: condPath + ".attr", Message: `operator "gt" needs an integer or number attribute, "network_id" is string`},
		},
		"starts_with on an integer attribute": {
			rules: rule(`{"attr": "age_days", "op": "not_starts_with", "value": "1"}`),
			want:  validate.Problem{Path: condPath + ".attr", Message: `operator "not_starts_with" needs a string attribute, "age_days" is integer`},
		},
		"invalid regular expression": {
			rules: rule(`{"attr": "player_id", "op": "matches", "value": "("}`),
			want:  validate.Problem{Path: condPath + ".value", Message: "invalid regular expression \"(\": error parsing regexp: missing closing ): `(`"},
		},

		// Value shapes.
		"exists with a value": {
			rules: rule(`{"attr": "player_id", "op": "exists", "value": true}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `operator "exists" needs no value`},
		},
		"eq without a value": {
			rules: rule(`{"attr": "player_id", "op": "eq"}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `operator "eq" needs a value`},
		},
		"eq with a list": {
			rules: rule(`{"attr": "player_id", "op": "eq", "value": ["P-1"]}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `operator "eq" needs a single value, not a list`},
		},
		"in with an empty list": {
			rules: rule(`{"attr": "player_id", "op": "in", "value": []}`),
			want:  validate.Problem{Path: condPath + ".value", Message: `operator "in" needs a non-empty list`},
		},
		"percentage over 100": {
			rules: `[{"name": "r", "if": [], "then": true, "rollout": {"percentage": 101, "bucket_by": "network_id"}}]`,
			want:  validate.Problem{Path: `flags.f.rules[0] "r".rollout.percentage`, Message: "percentage 101 is outside 0..100"},
		},
		"empty rule name": {
			rules: `[{"name": "", "if": [], "then": true}]`,
			want:  validate.Problem{Path: `flags.f.rules[0] ""`, Message: "rule name must be 1 to 100 characters"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := validate.Validate(flagsDoc(t, tc.groups, tc.rules), parseCatalog(t, catalogJSON))
			assert.Equal(t, []validate.Problem{tc.want}, problems(t, err))
		})
	}
}

func TestValidate_GroupOf200ValuesIsValid(t *testing.T) {
	groups := `{"big": {"attr": "network_id", "values": ` + values(validate.MaxGroupValues) + `}}`
	_, err := validate.Validate(flagsDoc(t, groups, ""), parseCatalog(t, catalogJSON))
	assert.NoError(t, err)
}

func TestValidate_FlagFields(t *testing.T) {
	doc := &document.Flags{
		SchemaVersion: 1,
		Flags: map[string]document.Flag{
			"New-Flow": {Owner: "", Jira: "atom-54", RemoveAfter: "2026-02-31"},
		},
	}
	_, err := validate.Validate(doc, parseCatalog(t, catalogJSON))
	assert.Equal(t, []validate.Problem{
		{Path: "flags.New-Flow", Message: `flag name "New-Flow" must match ^[a-z][a-z0-9_]{0,63}$`},
		{Path: "flags.New-Flow.owner", Message: "owner is required"},
		{Path: "flags.New-Flow.jira", Message: `jira "atom-54" must look like PROJ-123`},
		{Path: "flags.New-Flow.remove_after", Message: `remove_after "2026-02-31" must be a date (YYYY-MM-DD)`},
	}, problems(t, err))
}

func TestValidate_GoValuesBuiltInCode(t *testing.T) {
	doc := &document.Flags{
		SchemaVersion: 1,
		Groups:        map[string]document.Group{"g": {Attr: "age_days", Values: []any{1, int64(2), 3.0}}},
		Flags: map[string]document.Flag{"f": {
			Owner: "ivo", Jira: "ATOM-54", RemoveAfter: "2027-03-01", Enabled: true,
			Rules: []document.Rule{{Name: "r", Then: true, If: []document.Condition{
				{Attr: "stake", Op: "gt", Value: float32(1.5)},
				{Attr: "age_days", Op: "lt", Value: uint8(9)},
				{Attr: "age_days", Op: "eq", Value: 2.5},
			}}},
		}},
	}
	_, err := validate.Validate(doc, parseCatalog(t, catalogJSON))
	assert.Equal(t, []validate.Problem{
		{Path: `flags.f.rules[0] "r".if[2].value`, Message: "value 2.5 is not a valid integer"},
	}, problems(t, err))
}

func TestValidate_CatalogChangeBreakingALiveFlag(t *testing.T) {
	live := flagsDoc(t, "", `[
  {"name": "big stakes", "if": [{"attr": "stake", "op": "gt", "value": 100}], "then": true},
  {"name": "vips", "if": [{"attr": "player_id", "op": "is_vip", "value": true}], "then": true}
]`)
	_, err := validate.Validate(live, parseCatalog(t, catalogJSON))
	require.NoError(t, err, "the live flags are valid against the current catalog")

	cases := map[string]struct {
		catalog string
		want    []validate.Problem
	}{
		"attribute and operator removed": {
			catalog: `{"schema_version": 1, "attributes": {"player_id": {"type": "string"}}}`,
			want: []validate.Problem{
				{Path: `flags.f.rules[0] "big stakes".if[0].attr`, Message: `attribute "stake" is not in the catalog`},
				{Path: `flags.f.rules[1] "vips".if[0].op`, Message: `operator "is_vip" is neither built-in nor in the catalog`},
			},
		},
		"attribute retyped": {
			catalog: `{"schema_version": 1, "attributes": {"player_id": {"type": "string"}, "stake": {"type": "string"}},
			           "operators": {"is_vip": {"value_type": "boolean"}}}`,
			want: []validate.Problem{
				{Path: `flags.f.rules[0] "big stakes".if[0].attr`, Message: `operator "gt" needs an integer or number attribute, "stake" is string`},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := validate.Validate(live, parseCatalog(t, tc.catalog))
			assert.Equal(t, tc.want, problems(t, err))
		})
	}
}

func TestValidate_DuplicateRuleNameIsAWarning(t *testing.T) {
	doc := flagsDoc(t, "", `[
  {"name": "canary", "if": [], "then": true},
  {"name": "other", "if": [], "then": true},
  {"name": "canary", "if": [], "then": false}
]`)
	warnings, err := validate.Validate(doc, parseCatalog(t, catalogJSON))
	require.NoError(t, err)
	assert.Equal(t, []validate.Problem{{
		Path:    `flags.f.rules[2] "canary"`,
		Message: "same name as rules[0]; rules run in order, so this one only applies to callers rules[0] did not match",
	}}, warnings)
}

func TestValidate_ReportsEveryProblemInOrder(t *testing.T) {
	doc := &document.Flags{
		SchemaVersion: 1,
		Groups:        map[string]document.Group{"b-group": {Attr: "plan", Values: []any{"x"}}},
		Flags: map[string]document.Flag{
			"zeta":  {Owner: "ivo", Jira: "ATOM-1", RemoveAfter: "2027-01-01", Rules: []document.Rule{{Name: "r", If: []document.Condition{{Attr: "plan", Op: "exists"}}}}},
			"alpha": {Owner: "ivo", Jira: "ATOM-1", RemoveAfter: "2027-01-01", Rules: []document.Rule{{Name: "r", If: []document.Condition{{Attr: "site", Op: "exists"}}}}},
		},
	}
	catalog := parseCatalog(t, catalogJSON)
	_, first := validate.Validate(doc, catalog)
	got := problems(t, first)
	assert.Equal(t, []string{
		"groups.b-group.attr",
		`flags.alpha.rules[0] "r".if[0].attr`,
		`flags.zeta.rules[0] "r".if[0].attr`,
	}, paths(got))

	for i := 0; i < 20; i++ {
		_, again := validate.Validate(doc, catalog)
		require.Equal(t, got, problems(t, again))
	}
}

func paths(ps []validate.Problem) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Path
	}
	return out
}

func TestValidate_ChecksTheCatalog(t *testing.T) {
	catalog := &document.Catalog{
		SchemaVersion: 1,
		Attributes: map[string]document.Attribute{
			"player_id": {Type: document.TypeString},
			"Bad":       {Type: "date"},
		},
		Operators: map[string]document.Operator{
			"not_eq":  {ValueType: document.TypeString},
			"not_vip": {ValueType: document.TypeBoolean},
		},
		Resolvers: map[string]document.Resolver{"network_id": {Requires: []string{"player_id", "session_id"}}},
	}
	want := []validate.Problem{
		{Path: "catalog.attributes.Bad", Message: `attribute name "Bad" must match ^[a-z][a-z0-9_]{0,63}$`},
		{Path: "catalog.attributes.Bad", Message: `type "date" must be string, integer, number or boolean`},
		{Path: "catalog.operators.not_eq", Message: `custom operator "not_eq" has the name of a built-in operator`},
		{Path: "catalog.operators.not_vip", Message: `custom operator "not_vip" starts with not_, which negates an operator`},
		{Path: "catalog.resolvers.network_id", Message: `resolver for "network_id": attribute is not in the catalog`},
		{Path: "catalog.resolvers.network_id.requires[1]", Message: `attribute "session_id" is not in the catalog`},
	}

	assert.Equal(t, want, problems(t, validate.ValidateCatalog(catalog)))

	_, err := validate.Validate(&document.Flags{SchemaVersion: 1}, catalog)
	assert.Equal(t, want, problems(t, err))
}

func TestValidate_NilDocuments(t *testing.T) {
	_, err := validate.Validate(nil, parseCatalog(t, catalogJSON))
	assert.EqualError(t, err, "validate: feature-flags document is nil")

	_, err = validate.Validate(&document.Flags{}, nil)
	assert.EqualError(t, err, "validate: catalog document is nil")

	assert.EqualError(t, validate.ValidateCatalog(nil), "validate: catalog document is nil")
}

func TestError_Message(t *testing.T) {
	err := &validate.Error{Problems: []validate.Problem{
		{Path: "groups.g.attr", Message: `attribute "plan" is not in the catalog`},
		{Path: "flags.f.jira", Message: `jira "x" must look like PROJ-123`},
	}}
	assert.Equal(t, "feature flags are invalid (2 problems):\n"+
		"groups.g.attr: attribute \"plan\" is not in the catalog\n"+
		"flags.f.jira: jira \"x\" must look like PROJ-123", err.Error())

	var verr *validate.Error
	assert.True(t, errors.As(fmt.Errorf("deploy: %w", err), &verr))
}

func TestValidate_GroupOfLargeIDs(t *testing.T) {
	catalog := parseCatalog(t, `{"schema_version": 1, "attributes": {"player_no": {"type": "integer"}}}`)
	groups := `{"g": {"attr": "player_no", "values": [9007199254740992, 9007199254740993.0]}}`
	_, err := validate.Validate(flagsDoc(t, groups, ""), catalog)
	assert.NoError(t, err)
}

func TestValidateCatalogChange(t *testing.T) {
	live := flagsDoc(t, `{"staff": {"attr": "player_id", "values": ["P-1"]}}`, `[
  {"name": "staff", "if": [{"attr": "player_id", "op": "in_group", "value": "staff"}], "then": true},
  {"name": "has age", "if": [{"attr": "age_days", "op": "exists"}], "then": true},
  {"name": "10%", "if": [], "then": true, "rollout": {"percentage": 10, "bucket_by": "network_id"}}
]`)
	prev := parseCatalog(t, catalogJSON)

	cases := map[string]struct {
		next string
		want []validate.Problem
	}{
		"unchanged catalog": {next: catalogJSON},
		"unused attribute retyped": {
			next: strings.Replace(catalogJSON, `"stake": { "type": "number" }`, `"stake": { "type": "string" }`, 1),
		},
		"rollout attribute retyped": {
			next: strings.Replace(catalogJSON, `"network_id": { "type": "string" }`, `"network_id": { "type": "integer" }`, 1),
			want: []validate.Problem{{
				Path:    `flags.f.rules[2] "10%".rollout.bucket_by`,
				Message: `attribute "network_id" changed type from string to integer; callers sending the old type stop matching`,
			}},
		},
		"exists attribute retyped": {
			next: strings.Replace(catalogJSON, `"age_days": { "type": "integer" }`, `"age_days": { "type": "string" }`, 1),
			want: []validate.Problem{{
				Path:    `flags.f.rules[1] "has age".if[0].attr`,
				Message: `attribute "age_days" changed type from integer to string; callers sending the old type stop matching`,
			}},
		},
		"group attribute retyped": {
			next: strings.Replace(catalogJSON, `"player_id": { "type": "string" }`, `"player_id": { "type": "number" }`, 1),
			want: []validate.Problem{
				{Path: "groups.staff.values[0]", Message: `value "P-1" is not a valid number`},
				{Path: "groups.staff.attr", Message: `attribute "player_id" changed type from string to number; callers sending the old type stop matching`},
				{Path: `flags.f.rules[0] "staff".if[0].attr`, Message: `attribute "player_id" changed type from string to number; callers sending the old type stop matching`},
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			next := parseCatalog(t, tc.next)
			_, err := validate.ValidateCatalogChange(prev, next, live)
			if tc.want == nil {
				assert.NoError(t, err)
				return
			}
			assert.Equal(t, tc.want, problems(t, err))
		})
	}
}

func TestValidateCatalogChange_WithoutPreviousCatalog(t *testing.T) {
	live := flagsDoc(t, "", `[{"name": "10%", "if": [], "then": true, "rollout": {"percentage": 10, "bucket_by": "network_id"}}]`)
	next := parseCatalog(t, strings.Replace(catalogJSON, `"network_id": { "type": "string" }`, `"network_id": { "type": "integer" }`, 1))

	_, err := validate.ValidateCatalogChange(nil, next, live)
	assert.NoError(t, err)

	_, err = validate.ValidateCatalogChange(next, next, nil)
	assert.EqualError(t, err, "validate: feature-flags document is nil")
	_, err = validate.ValidateCatalogChange(next, nil, live)
	assert.EqualError(t, err, "validate: catalog document is nil")
}
