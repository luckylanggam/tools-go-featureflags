package document_test

import (
	"encoding/json"
	"math"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

// Literals that cost big.Rat milliseconds and megabytes if they reach it.
var hostileIntegers = map[string]string{
	"huge exponent":    "1e1000000",
	"tiny exponent":    "1e-1000000",
	"megabyte literal": "1" + strings.Repeat("0", 1<<20) + ".5",
}

func TestJSONInt64(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{in: "1", want: 1, ok: true},
		{in: "1.0", want: 1, ok: true},
		{in: "1e3", want: 1000, ok: true},
		{in: "-0.0", want: 0, ok: true},
		{in: "9007199254740993.0", want: 9007199254740993, ok: true},
		{in: "9223372036854775807", want: math.MaxInt64, ok: true},
		{in: "9223372036854775808"},
		{in: "-9223372036854775808", want: math.MinInt64, ok: true},
		{in: "1.5"},
		{in: "1e400"},
		{in: "1e31"},
		{in: "1e1000000"},
		{in: "1e-1000000"},
		// Guard edges: exponent ±30 and a 100-character literal still parse.
		{in: "1E1", want: 10, ok: true},
		{in: "100000000000000000000000000000e-30", want: 0},
		{in: "1000000000000000000000000000000e-30", want: 1, ok: true},
		{in: "1." + strings.Repeat("0", 98), want: 1, ok: true},
		{in: "1." + strings.Repeat("0", 99)},
		{in: "1e+"},
		{in: `"1"`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := document.JSONInt64(json.Number(tc.in))
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// Rejecting a hostile literal must not allocate: big.Rat is never reached.
func TestJSONInt64_RejectsHostileLiteralsWithoutParsing(t *testing.T) {
	for name, lit := range hostileIntegers {
		t.Run(name, func(t *testing.T) {
			allocs := testing.AllocsPerRun(10, func() {
				_, ok := document.JSONInt64(json.Number(lit))
				assert.False(t, ok)
			})
			assert.Zero(t, allocs)
		})
	}
}

// Decoding stays linear in the input. Without the guard the two parses below
// allocate 3-7 MB for the exponents and about 5 GB for the megabyte literal.
func TestParse_HostileIntegersStayCheap(t *testing.T) {
	for name, lit := range hostileIntegers {
		t.Run(name, func(t *testing.T) {
			maxAlloc := uint64(1<<20 + 64*len(lit))
			flags := []byte(`{"schema_version": 1, "flags": {"x": {"owner": "o", "jira": "AB-1",
				"remove_after": "2027-03-01", "enabled": true, "default": false,
				"rules": [{"name": "r", "if": [], "then": true, "rollout": {"percentage": ` + lit + `, "bucket_by": "network_id"}}]}}}`)
			catalog := []byte(`{"schema_version": ` + lit + `, "attributes": {}}`)

			alloc := allocated(func() {
				_, err := document.ParseFlags(flags)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "percentage: ")
				assert.Contains(t, err.Error(), " is not an integer")
				assert.Less(t, len(err.Error()), 300)

				_, err = document.ParseCatalog(catalog)
				require.Error(t, err)
				assert.Contains(t, err.Error(), "schema_version: ")
				assert.Less(t, len(err.Error()), 300)
			})
			assert.Less(t, alloc, maxAlloc)
		})
	}
}

func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}
