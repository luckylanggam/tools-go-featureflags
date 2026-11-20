package validate

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

// hasType reports whether a value written in a document has the JSON type
// of t. It is strict on purpose: a string attribute takes "101", not 101.
// Numbers may arrive as json.Number (parsed documents) or Go numbers
// (documents built in code); integers must fit int64.
func hasType(v any, t document.Type) bool {
	switch t {
	case document.TypeString:
		_, ok := v.(string)
		return ok
	case document.TypeInteger:
		return isInteger(v)
	case document.TypeNumber:
		return isNumber(v)
	case document.TypeBoolean:
		_, ok := v.(bool)
		return ok
	}
	return false
}

func isInteger(v any) bool {
	_, ok := toInt64(v)
	return ok
}

// toInt64 converts an integral number to int64. A json.Number is read
// exactly, never through float64, so large IDs keep their value.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int8:
		return int64(n), true
	case int16:
		return int64(n), true
	case int32:
		return int64(n), true
	case int64:
		return n, true
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint:
		return int64(n), uint64(n) <= math.MaxInt64
	case uint64:
		return int64(n), n <= math.MaxInt64
	case float32:
		return floatToInt64(float64(n))
	case float64:
		return floatToInt64(n)
	case json.Number:
		return document.JSONInt64(n)
	}
	return 0, false
}

func floatToInt64(f float64) (int64, bool) {
	if f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 {
		return 0, false
	}
	return int64(f), true
}

func isNumber(v any) bool {
	_, ok := toFloat64(v)
	return ok
}

func toFloat64(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float32:
		f = float64(n)
	case float64:
		f = n
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return 0, false
		}
		f = parsed
	case uint64:
		f = float64(n)
	case uint:
		f = float64(n)
	default:
		i, ok := toInt64(v)
		if !ok {
			return 0, false
		}
		f = float64(i)
	}
	return f, !math.IsNaN(f) && !math.IsInf(f, 0)
}

// valueKey identifies a value for duplicate detection within one list, so
// 1, 1.0 and json.Number("1e0") count as the same value. Any value works,
// including lists and objects a malformed document may hold.
func valueKey(v any) string {
	switch n := v.(type) {
	case string:
		return "s:" + n
	case bool:
		return fmt.Sprintf("b:%t", n)
	}
	if i, ok := toInt64(v); ok {
		return "i:" + strconv.FormatInt(i, 10)
	}
	if f, ok := toFloat64(v); ok {
		return "f:" + strconv.FormatFloat(f, 'g', -1, 64)
	}
	return fmt.Sprintf("v:%#v", v)
}

// display renders a document value for a message: strings quoted, so "5"
// and 5 read differently.
func display(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprint(v)
}
