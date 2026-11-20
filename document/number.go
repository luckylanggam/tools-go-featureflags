package document

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
)

// Limits on a JSON number literal before it is parsed exactly. No int64 is
// written longer or with a larger exponent, and big.Rat spends milliseconds
// and megabytes on long literals and on exponents near its own cap.
const (
	maxIntLiteralLen = 100
	maxIntExponent   = 30
)

// JSONInt64 reads a JSON number as an int64 if it is exactly one, without
// rounding through float64: "1.0", "1e3" and "9007199254740993.0" are;
// "1.5" and anything outside int64 are not. Literals longer than 100
// characters or with an exponent beyond ±30 are rejected before any exact
// parsing, so hostile input stays cheap.
func JSONInt64(n json.Number) (int64, bool) {
	s := string(n)
	if !plausibleInt(s) {
		return 0, false
	}
	if i, err := n.Int64(); err == nil {
		return i, true
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || !r.IsInt() || !r.Num().IsInt64() {
		return 0, false
	}
	return r.Num().Int64(), true
}

func plausibleInt(s string) bool {
	if len(s) > maxIntLiteralLen {
		return false
	}
	i := strings.IndexAny(s, "eE")
	if i < 0 {
		return true
	}
	exp, err := strconv.Atoi(s[i+1:])
	return err == nil && exp >= -maxIntExponent && exp <= maxIntExponent
}
