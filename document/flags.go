package document

import "encoding/json"

const flagsKind = "feature-flags"

// Flags is the feature-flags document: every flag and group of one project
// in one AppConfig hosted version, so a group and the rule using it always
// deploy together.
type Flags struct {
	SchemaVersion int              `json:"schema_version"`
	Groups        map[string]Group `json:"groups,omitempty"`
	Flags         map[string]Flag  `json:"flags"`
}

// Group is a named list of up to 200 values of one attribute.
type Group struct {
	Attr        string `json:"attr"`
	Description string `json:"description,omitempty"`
	Values      []any  `json:"values"`
}

// Flag is one flag. When Enabled is false (kill switch) the flag is off for
// every caller and its rules are skipped. Otherwise the first matching rule
// decides, and Default applies when none matches.
type Flag struct {
	Description string `json:"description,omitempty"`
	Owner       string `json:"owner"`
	Jira        string `json:"jira"`
	RemoveAfter string `json:"remove_after"`
	Enabled     bool   `json:"enabled"`
	Rules       []Rule `json:"rules,omitempty"`
	Default     bool   `json:"default"`
}

// Rule matches when every condition in If holds; an empty If matches every
// caller. Without Rollout all matching callers get Then. With Rollout only
// the given percentage of them get Then, and the rest do not match the rule,
// so evaluation continues with the next rule.
type Rule struct {
	Name    string      `json:"name"`
	If      []Condition `json:"if"`
	Then    bool        `json:"then"`
	Rollout *Rollout    `json:"rollout,omitempty"`
}

// Condition compares one attribute with Value using Op, a built-in or a
// custom operator declared in the catalog. Value is absent for exists and
// not_exists, a list for in and not_in, and a single value otherwise.
type Condition struct {
	Attr  string `json:"attr"`
	Op    string `json:"op"`
	Value any    `json:"value,omitempty"`
}

// Rollout limits a rule to a stable percentage of callers, bucketed by the
// value of the BucketBy attribute.
type Rollout struct {
	Percentage int    `json:"percentage"`
	BucketBy   string `json:"bucket_by"`
}

// UnmarshalJSON decodes Flags strictly, accepting schema_version written as
// any integral number.
func (d *Flags) UnmarshalJSON(data []byte) error {
	type fields Flags
	var raw struct {
		fields
		SchemaVersion json.RawMessage `json:"schema_version"`
	}
	if err := strictDecoder(data).Decode(&raw); err != nil {
		return err
	}
	version, err := integer("schema_version", raw.SchemaVersion)
	if err != nil {
		return err
	}
	*d = Flags(raw.fields)
	d.SchemaVersion = version
	return nil
}

// UnmarshalJSON decodes a Rollout strictly, accepting percentage written as
// any integral number.
func (r *Rollout) UnmarshalJSON(data []byte) error {
	type fields Rollout
	var raw struct {
		fields
		Percentage json.RawMessage `json:"percentage"`
	}
	if err := strictDecoder(data).Decode(&raw); err != nil {
		return err
	}
	percentage, err := integer("percentage", raw.Percentage)
	if err != nil {
		return err
	}
	*r = Rollout(raw.fields)
	r.Percentage = percentage
	return nil
}

// ParseFlags decodes a feature-flags document. It rejects unknown fields and
// any schema_version other than SchemaVersion; it does not apply the JSON
// Schema or check references.
func ParseFlags(data []byte) (*Flags, error) {
	var doc Flags
	if err := decodeStrict(flagsKind, data, &doc); err != nil {
		return nil, err
	}
	if err := checkVersion(flagsKind, doc.SchemaVersion); err != nil {
		return nil, err
	}
	return &doc, nil
}
