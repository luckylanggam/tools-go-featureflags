package document

import "encoding/json"

const catalogKind = "catalog"

// Type is the declared type of an attribute or of a custom operator's value.
type Type string

const (
	TypeString  Type = "string"
	TypeInteger Type = "integer"
	TypeNumber  Type = "number"
	TypeBoolean Type = "boolean"
)

// Catalog declares what a project's flags may use: attributes with their
// types, custom operators and attribute resolvers. It lives in the project
// repository and is published to AppConfig by CI.
type Catalog struct {
	SchemaVersion int                  `json:"schema_version"`
	Attributes    map[string]Attribute `json:"attributes"`
	Operators     map[string]Operator  `json:"operators,omitempty"`
	Resolvers     map[string]Resolver  `json:"resolvers,omitempty"`
}

// Attribute is one attribute callers may send.
type Attribute struct {
	Type        Type   `json:"type"`
	Description string `json:"description,omitempty"`
}

// Operator is a custom operator registered in code; ValueType is the type
// of the condition value it takes.
type Operator struct {
	ValueType   Type   `json:"value_type"`
	Description string `json:"description,omitempty"`
}

// Resolver fetches the attribute it is keyed by when a rule needs it and the
// caller did not send it. Requires lists the attributes it reads.
type Resolver struct {
	Requires    []string `json:"requires,omitempty"`
	Description string   `json:"description,omitempty"`
}

// UnmarshalJSON decodes a Catalog strictly, accepting schema_version written
// as any integral number.
func (c *Catalog) UnmarshalJSON(data []byte) error {
	type fields Catalog
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
	*c = Catalog(raw.fields)
	c.SchemaVersion = version
	return nil
}

// ParseCatalog decodes a catalog document. It rejects unknown fields and any
// schema_version other than SchemaVersion; it does not apply the JSON Schema
// or check references.
func ParseCatalog(data []byte) (*Catalog, error) {
	var doc Catalog
	if err := decodeStrict(catalogKind, data, &doc); err != nil {
		return nil, err
	}
	if err := checkVersion(catalogKind, doc.SchemaVersion); err != nil {
		return nil, err
	}
	return &doc, nil
}
