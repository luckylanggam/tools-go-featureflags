package engine

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

// Evaluation must not do network or other I/O: the package may only import
// these. Adding an import here needs a reason. The OpenTelemetry API only
// records in memory; exporting is the service's meter provider's job.
var allowedImports = []string{
	"context",
	"encoding/json",
	"fmt",
	"hash/fnv",
	"maps",
	"math",
	"math/big",
	"regexp",
	"slices",
	"strconv",
	"strings",
	"sync",
	"time",
	"go.opentelemetry.io/otel",
	"go.opentelemetry.io/otel/attribute",
	"go.opentelemetry.io/otel/metric",
	"github.com/luckylanggam/tools-go-featureflags/document",
	"github.com/luckylanggam/tools-go-featureflags/errors",
}

func TestNoIOImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		require.NoError(t, err)
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			require.NoError(t, err)
			assert.Contains(t, allowedImports, path, "%s imports %s", name, path)
		}
	}
}

// The engine implements exactly the operators the catalog schema reserves
// as built-in.
func TestBuiltinOperatorsMatchSchema(t *testing.T) {
	var schema struct {
		Properties struct {
			Operators struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"operators"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(document.CatalogSchema(), &schema))
	var reserved []string
	for name := range schema.Properties.Operators.Properties {
		reserved = append(reserved, name)
	}
	slices.Sort(reserved)

	implemented := []string{opExists, opNotExists}
	for name := range operators {
		implemented = append(implemented, name, negPrefix+name)
	}
	slices.Sort(implemented)

	assert.Equal(t, reserved, implemented)
	assert.Len(t, implemented, 18)
}
