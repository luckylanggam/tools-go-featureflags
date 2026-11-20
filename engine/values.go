package engine

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

// normalize converts v to the Go form of the catalog type t: string,
// int64, float64 or bool. The second result is false when v has no
// lossless form of that type.
//
//   - string  <- string, or an integral number (101 -> "101")
//   - integer <- any integral number, or a base-10 integer string
//   - number  <- any finite number, or a numeric string
//   - boolean <- bool only
func normalize(v any, t document.Type) (any, bool) {
	switch t {
	case document.TypeString:
		return toString(v)
	case document.TypeInteger:
		return toInteger(v)
	case document.TypeNumber:
		return toNumber(v)
	case document.TypeBoolean:
		b, ok := v.(bool)
		return b, ok
	}
	return nil, false
}

func toString(v any) (any, bool) {
	// json.Number is a string type, so it must be matched before string.
	if n, ok := v.(json.Number); ok {
		i, ok := toInteger(n)
		if !ok {
			return nil, false
		}
		return strconv.FormatInt(i.(int64), 10), true
	}
	if s, ok := v.(string); ok {
		return s, true
	}
	i, ok := toInteger(v)
	if !ok {
		return nil, false
	}
	return strconv.FormatInt(i.(int64), 10), true
}

func toInteger(v any) (any, bool) {
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
	case uint:
		return uintToInt64(uint64(n))
	case uint8:
		return int64(n), true
	case uint16:
		return int64(n), true
	case uint32:
		return int64(n), true
	case uint64:
		return uintToInt64(n)
	case float32:
		return floatToInt64(float64(n))
	case float64:
		return floatToInt64(n)
	case json.Number:
		i, ok := document.JSONInt64(n)
		if !ok {
			return nil, false
		}
		return i, true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		if err != nil {
			return nil, false
		}
		return i, true
	}
	return nil, false
}

func toNumber(v any) (any, bool) {
	var f float64
	switch n := v.(type) {
	case float32:
		f = float64(n)
	case float64:
		f = n
	case uint:
		f = float64(n)
	case uint64:
		f = float64(n)
	case json.Number:
		parsed, err := n.Float64()
		if err != nil {
			return nil, false
		}
		f = parsed
	case string:
		parsed, err := strconv.ParseFloat(n, 64)
		if err != nil {
			return nil, false
		}
		f = parsed
	default:
		i, ok := toInteger(v)
		if !ok {
			return nil, false
		}
		f = float64(i.(int64))
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, false
	}
	// -0 and 0 are the same number; keep one form so they share a bucket.
	if f == 0 {
		f = 0
	}
	return f, true
}

func uintToInt64(u uint64) (any, bool) {
	if u > math.MaxInt64 {
		return nil, false
	}
	return int64(u), true
}

func floatToInt64(f float64) (any, bool) {
	// -2^63 is exact in float64; 2^63 is the first value out of range.
	if f != math.Trunc(f) || f < math.MinInt64 || f >= math.MaxInt64 {
		return nil, false
	}
	return int64(f), true
}

// canonical renders a normalized value as the string used for bucketing,
// so the same caller lands in the same bucket whatever Go type it arrives as.
func canonical(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case int64:
		return strconv.FormatInt(n, 10)
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case bool:
		return strconv.FormatBool(n)
	}
	return ""
}
