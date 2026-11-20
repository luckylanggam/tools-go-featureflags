// Package document defines schema_version 1 of the two documents stored in
// AWS AppConfig: the feature-flags document (flags and groups) and the
// project catalog (attributes, custom operators and resolvers).
//
// The embedded JSON Schemas check the shape of each document. Checks that
// cross references (a group exists, an attribute is in the catalog, value
// types) belong to Validate, not to the schemas or to the parsers here.
package document

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

// SchemaVersion is the only document version this package parses.
const SchemaVersion = 1

//go:embed schemas/*.json
var schemas embed.FS

// FlagsSchema returns the draft-04 JSON Schema of the feature-flags document.
func FlagsSchema() []byte {
	return mustReadSchema("schemas/feature-flags.v1.json")
}

// CatalogSchema returns the draft-04 JSON Schema of the catalog document.
func CatalogSchema() []byte {
	return mustReadSchema("schemas/catalog.v1.json")
}

func mustReadSchema(name string) []byte {
	b, err := schemas.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("embedded schema %s missing: %v", name, err))
	}
	return b
}

// decodeStrict decodes exactly one JSON object into v, rejecting unknown
// fields and trailing data.
func decodeStrict(kind string, data []byte, v any) error {
	dec := strictDecoder(data)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf(ffErrors.ErrParseDocument, kind, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf(ffErrors.ErrDocumentTrailingData, kind)
	}
	return nil
}

// strictDecoder rejects unknown fields and keeps numbers as json.Number so
// integer attribute values (e.g. IDs) are not rounded through float64.
func strictDecoder(data []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	return dec
}

// integer decodes a JSON number with no fractional part into an int. It
// accepts every form the schemas accept as an integer (1, 1.0, 1e0), which a
// plain int field rejects. An absent field decodes to 0.
func integer(field string, raw json.RawMessage) (int, error) {
	if raw == nil {
		return 0, nil
	}
	i, ok := JSONInt64(json.Number(raw))
	if !ok {
		return 0, fmt.Errorf(ffErrors.ErrNotInteger, field, clip(raw))
	}
	return int(i), nil
}

// maxErrorValueLen caps how much of a rejected raw value an error repeats, so
// an oversized literal does not produce an equally oversized error.
const maxErrorValueLen = 32

// clip returns raw as a string, cut to maxErrorValueLen bytes on a rune
// boundary and followed by "…" when it is longer.
func clip(raw []byte) string {
	if len(raw) <= maxErrorValueLen {
		return string(raw)
	}
	n := maxErrorValueLen
	for n > 0 && !utf8.RuneStart(raw[n]) {
		n--
	}
	return string(raw[:n]) + "…"
}

func checkVersion(kind string, version int) error {
	if version != SchemaVersion {
		return fmt.Errorf(ffErrors.ErrUnsupportedSchemaVersion, kind, version, SchemaVersion)
	}
	return nil
}
