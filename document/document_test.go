package document_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

// AppConfig rejects JSON Schema validators longer than this.
const appConfigSchemaMaxChars = 32768

const draft04 = "http://json-schema.org/draft-04/schema#"

func compileSchema(t *testing.T, name string, schema []byte) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schema))
	require.NoError(t, err)
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft4)
	require.NoError(t, c.AddResource(name, doc))
	s, err := c.Compile(name)
	require.NoError(t, err)
	return s
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return b
}

func validateFixture(t *testing.T, s *jsonschema.Schema, data []byte) error {
	t.Helper()
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	require.NoError(t, err)
	return s.Validate(inst)
}

// errorLocations lists the instance location of every error in the tree,
// as JSON pointers ("" is the document root).
func errorLocations(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	var locs []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		loc := ""
		if len(e.InstanceLocation) > 0 {
			loc = "/" + strings.Join(e.InstanceLocation, "/")
		}
		locs = append(locs, loc)
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return locs
}

func fixtureNames(t *testing.T, dir string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".json"))
	}
	return names
}

func TestSchemas_AppConfigConstraints(t *testing.T) {
	for name, schema := range map[string][]byte{
		"feature-flags": document.FlagsSchema(),
		"catalog":       document.CatalogSchema(),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Less(t, len([]rune(string(schema))), appConfigSchemaMaxChars)

			var meta struct {
				Schema string `json:"$schema"`
			}
			require.NoError(t, json.Unmarshal(schema, &meta))
			assert.Equal(t, draft04, meta.Schema)

			compileSchema(t, name, schema)
		})
	}
}

func TestFlags_ValidFixtures(t *testing.T) {
	s := compileSchema(t, "feature-flags", document.FlagsSchema())
	dir := filepath.Join("testdata", "flags", "valid")
	for _, name := range fixtureNames(t, dir) {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, filepath.Join(dir, name+".json"))
			require.NoError(t, validateFixture(t, s, data))

			doc, err := document.ParseFlags(data)
			require.NoError(t, err)
			assert.Equal(t, document.SchemaVersion, doc.SchemaVersion)
		})
	}
}

func TestFlags_InvalidFixtures(t *testing.T) {
	s := compileSchema(t, "feature-flags", document.FlagsSchema())
	dir := filepath.Join("testdata", "flags", "invalid")

	// Where the schema must report the error, and whether ParseFlags rejects
	// the fixture too (it only checks unknown fields and schema_version).
	cases := map[string]struct {
		location string
		parseErr bool
	}{
		"missing-flags":               {location: ""},
		"wrong-schema-version":        {location: "/schema_version", parseErr: true},
		"schema-version-string":       {location: "/schema_version", parseErr: true},
		"unknown-top-level-field":     {location: "", parseErr: true},
		"flag-name-upper":             {location: "/flags"},
		"flag-name-too-long":          {location: "/flags"},
		"flag-missing-owner":          {location: "/flags/x"},
		"flag-missing-enabled":        {location: "/flags/x"},
		"flag-unknown-field":          {location: "/flags/x", parseErr: true},
		"flag-bad-jira":               {location: "/flags/x/jira"},
		"flag-bad-remove-after":       {location: "/flags/x/remove_after"},
		"flag-enabled-string":         {location: "/flags/x/enabled", parseErr: true},
		"rule-missing-then":           {location: "/flags/x/rules/0"},
		"rule-missing-if":             {location: "/flags/x/rules/0"},
		"rule-missing-name":           {location: "/flags/x/rules/0"},
		"rollout-over-100":            {location: "/flags/x/rules/0/rollout/percentage"},
		"rollout-fraction":            {location: "/flags/x/rules/0/rollout/percentage", parseErr: true},
		"rollout-missing-bucket-by":   {location: "/flags/x/rules/0/rollout"},
		"condition-bad-attr-name":     {location: "/flags/x/rules/0/if/0/attr"},
		"condition-bad-op-name":       {location: "/flags/x/rules/0/if/0/op"},
		"condition-exists-with-value": {location: "/flags/x/rules/0/if/0"},
		"condition-eq-missing-value":  {location: "/flags/x/rules/0/if/0"},
		"condition-in-scalar":         {location: "/flags/x/rules/0/if/0/value"},
		"condition-in-empty":          {location: "/flags/x/rules/0/if/0/value"},
		"condition-eq-list":           {location: "/flags/x/rules/0/if/0/value"},
		"condition-value-object":      {location: "/flags/x/rules/0/if/0/value"},
		"group-201-values":            {location: "/groups/g/values"},
		"group-empty":                 {location: "/groups/g/values"},
		"group-duplicate-values":      {location: "/groups/g/values"},
		"group-name-underscore":       {location: "/groups"},
		"group-missing-attr":          {location: "/groups/g"},
	}
	assert.ElementsMatch(t, fixtureNames(t, dir), mapKeys(cases), "every invalid fixture needs a case")

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, filepath.Join(dir, name+".json"))
			err := validateFixture(t, s, data)
			require.Error(t, err)
			assert.Contains(t, errorLocations(err), tc.location, err.Error())

			_, err = document.ParseFlags(data)
			if tc.parseErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCatalog_ValidFixtures(t *testing.T) {
	s := compileSchema(t, "catalog", document.CatalogSchema())
	dir := filepath.Join("testdata", "catalog", "valid")
	for _, name := range fixtureNames(t, dir) {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, filepath.Join(dir, name+".json"))
			require.NoError(t, validateFixture(t, s, data))

			doc, err := document.ParseCatalog(data)
			require.NoError(t, err)
			assert.Equal(t, document.SchemaVersion, doc.SchemaVersion)
		})
	}
}

func TestCatalog_InvalidFixtures(t *testing.T) {
	s := compileSchema(t, "catalog", document.CatalogSchema())
	dir := filepath.Join("testdata", "catalog", "invalid")

	cases := map[string]struct {
		location string
		parseErr bool
	}{
		"missing-attributes":          {location: ""},
		"wrong-schema-version":        {location: "/schema_version", parseErr: true},
		"schema-version-string":       {location: "/schema_version", parseErr: true},
		"attribute-bad-type":          {location: "/attributes/stake/type"},
		"attribute-missing-type":      {location: "/attributes/stake"},
		"attribute-bad-name":          {location: "/attributes"},
		"attribute-unknown-field":     {location: "/attributes/stake", parseErr: true},
		"operator-shadows-builtin":    {location: "/operators/eq"},
		"operator-missing-value-type": {location: "/operators/is_vip"},
		"resolver-bad-requires":       {location: "/resolvers/network_id/requires/0"},
		"resolver-duplicate-requires": {location: "/resolvers/network_id/requires"},
	}
	assert.ElementsMatch(t, fixtureNames(t, dir), mapKeys(cases), "every invalid fixture needs a case")

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			data := readFixture(t, filepath.Join(dir, name+".json"))
			err := validateFixture(t, s, data)
			require.Error(t, err)
			assert.Contains(t, errorLocations(err), tc.location, err.Error())

			_, err = document.ParseCatalog(data)
			if tc.parseErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseFlags_TDDExample(t *testing.T) {
	doc, err := document.ParseFlags(readFixture(t, filepath.Join("testdata", "flags", "valid", "tdd-example.json")))
	require.NoError(t, err)

	assert.Equal(t, document.Group{Attr: "network_id", Values: []any{"N-1", "N-2"}}, doc.Groups["canary-1"])

	flag := doc.Flags["new_withdraw_flow"]
	assert.Equal(t, "marco", flag.Owner)
	assert.Equal(t, "PHOENIX-900", flag.Jira)
	assert.Equal(t, "2027-03-01", flag.RemoveAfter)
	assert.True(t, flag.Enabled)
	assert.False(t, flag.Default)
	require.Len(t, flag.Rules, 2)
	assert.Equal(t, document.Rule{
		Name: "canary",
		If:   []document.Condition{{Attr: "network_id", Op: "in_group", Value: "canary-1"}},
		Then: true,
	}, flag.Rules[0])
	assert.Equal(t, document.Rule{
		Name:    "10% rollout",
		If:      []document.Condition{},
		Then:    true,
		Rollout: &document.Rollout{Percentage: 10, BucketBy: "network_id"},
	}, flag.Rules[1])
}

func TestParseFlags_KeepsExactNumbers(t *testing.T) {
	doc, err := document.ParseFlags([]byte(`{"schema_version": 1, "flags": {}, "groups": {"g": {"attr": "player_id", "values": [9007199254740993]}}}`))
	require.NoError(t, err)
	assert.Equal(t, []any{json.Number("9007199254740993")}, doc.Groups["g"].Values)
}

func TestParse_IntegralNumberForms(t *testing.T) {
	for _, n := range []string{"10", "10.0", "1e1", "1.0E1", "100e-1"} {
		t.Run(n, func(t *testing.T) {
			doc, err := document.ParseFlags([]byte(`{"schema_version": 1.0, "flags": {"x": {"owner": "o", "jira": "AB-1",
				"remove_after": "2027-03-01", "enabled": true, "default": false,
				"rules": [{"name": "r", "if": [], "then": true, "rollout": {"percentage": ` + n + `, "bucket_by": "network_id"}}]}}}`))
			require.NoError(t, err)
			assert.Equal(t, 10, doc.Flags["x"].Rules[0].Rollout.Percentage)
		})
	}

	for name, version := range map[string]string{"fraction": "1.5", "string": `"1"`, "null": "null", "boolean": "true"} {
		t.Run("schema_version "+name, func(t *testing.T) {
			_, err := document.ParseFlags([]byte(`{"schema_version": ` + version + `, "flags": {}}`))
			assert.Error(t, err)
			_, err = document.ParseCatalog([]byte(`{"schema_version": ` + version + `, "attributes": {}}`))
			assert.Error(t, err)
		})
	}

	_, err := document.ParseFlags([]byte(`{"schema_version": 2.0, "flags": {}}`))
	assert.EqualError(t, err, "feature-flags document: unsupported schema_version 2, want 1")
}

func TestParse_RejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty":         ``,
		"not json":      `schema_version: 1`,
		"array":         `[]`,
		"null":          `null`,
		"trailing data": `{"schema_version": 1} {}`,
		"no version":    `{}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := document.ParseFlags([]byte(input))
			assert.Error(t, err)
			_, err = document.ParseCatalog([]byte(input))
			assert.Error(t, err)
		})
	}
}

func TestParse_ErrorNamesTheDocument(t *testing.T) {
	_, err := document.ParseFlags([]byte(`{"schema_version": 2, "flags": {}}`))
	require.Error(t, err)
	assert.Equal(t, "feature-flags document: unsupported schema_version 2, want 1", err.Error())

	_, err = document.ParseCatalog([]byte(`{"schema_version": 1, "attributes": {}, "extra": true}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse catalog document:")

	_, err = document.ParseFlags([]byte(`{"schema_version": 1, "flags": {}} {}`))
	require.Error(t, err)
	assert.Equal(t, "parse feature-flags document: unexpected data after the JSON object", err.Error())

	_, err = document.ParseFlags([]byte(`{"schema_version": 1.5, "flags": {}}`))
	require.Error(t, err)
	assert.Equal(t, "parse feature-flags document: schema_version: 1.5 is not an integer", err.Error())
}

// A rejected value is repeated in the error only up to 32 bytes, cut on a rune
// boundary, so an oversized value cannot make the error just as large.
func TestParse_NotIntegerErrorClipsTheValue(t *testing.T) {
	cases := map[string]struct {
		value string
		want  string
	}{
		"32 bytes kept whole": {
			value: "1" + strings.Repeat("0", 29) + ".5",
			want:  "schema_version: 1" + strings.Repeat("0", 29) + ".5 is not an integer",
		},
		"33 bytes clipped": {
			value: "1" + strings.Repeat("0", 30) + ".5",
			want:  "schema_version: 1" + strings.Repeat("0", 30) + ".… is not an integer",
		},
		"megabyte literal": {
			value: "1" + strings.Repeat("0", 1<<20) + ".5",
			want:  "schema_version: 1" + strings.Repeat("0", 31) + "… is not an integer",
		},
		"multi-byte string cut on a rune boundary": {
			value: `"` + strings.Repeat("é", 40) + `"`,
			want:  `schema_version: "` + strings.Repeat("é", 15) + "… is not an integer",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := document.ParseCatalog([]byte(`{"schema_version": ` + tc.value + `, "attributes": {}}`))
			require.Error(t, err)
			assert.Equal(t, "parse catalog document: "+tc.want, err.Error())
			assert.True(t, utf8.ValidString(err.Error()))
		})
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
