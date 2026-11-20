package engine

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/luckylanggam/tools-go-featureflags/document"
)

func inRollout(flagName string, v any, percentage uint64) bool {
	return bucket(flagName, v) < percentage
}

func TestBucket_RaisingPercentageOnlyAdds(t *testing.T) {
	const ids = 10000
	prev := map[int]bool{}
	for pct := uint64(0); pct <= 100; pct += 5 {
		in := map[int]bool{}
		for id := 0; id < ids; id++ {
			if inRollout("new_withdraw_flow", fmt.Sprintf("p-%d", id), pct) {
				in[id] = true
			}
		}
		for id := range prev {
			assert.True(t, in[id], "id %d left the rollout when raising to %d%%", id, pct)
		}
		prev = in
	}
	assert.Len(t, prev, ids, "100% includes everyone")
}

func TestBucket_Distribution(t *testing.T) {
	const ids = 10000
	for _, pct := range []uint64{1, 10, 25, 50, 90} {
		n := 0
		for id := 0; id < ids; id++ {
			if inRollout("new_withdraw_flow", int64(id), pct) {
				n++
			}
		}
		got := float64(n) * 100 / ids
		assert.InDelta(t, float64(pct), got, 2, "%d%% rollout took %.2f%% of ids", pct, got)
	}
}

func TestBucket_FlagsAreIndependent(t *testing.T) {
	same := 0
	for id := 0; id < 1000; id++ {
		if inRollout("flag_a", int64(id), 50) == inRollout("flag_b", int64(id), 50) {
			same++
		}
	}
	// Independent 50% rollouts agree on about half the callers, not all.
	assert.InDelta(t, 500, same, 60)
}

func TestBucket_SameCallerWhateverTheGoType(t *testing.T) {
	for _, attrType := range []document.Type{document.TypeString, document.TypeInteger} {
		a, ok := normalize(101, attrType)
		assert.True(t, ok)
		b, ok := normalize("101", attrType)
		assert.True(t, ok)
		assert.Equal(t, bucket("f", a), bucket("f", b), attrType)
	}

	zero, ok := normalize(0.0, document.TypeNumber)
	assert.True(t, ok)
	for _, v := range []any{math.Copysign(0, -1), json.Number("-0.0"), "-0"} {
		negZero, ok := normalize(v, document.TypeNumber)
		assert.True(t, ok)
		assert.Equal(t, bucket("f", zero), bucket("f", negZero), "%T(%v)", v, v)
	}
}

// Pins the hash: a change here reshuffles every running rollout.
func TestBucket_Golden(t *testing.T) {
	tests := []struct {
		flag  string
		value any
		want  uint64
	}{
		{"new_withdraw_flow", "N-1", 93},
		{"new_withdraw_flow", "N-2", 60},
		{"new_withdraw_flow", int64(101), 49},
		{"other_flag", "N-1", 20},
		{"new_withdraw_flow", 1.5, 83},
		{"new_withdraw_flow", true, 7},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, bucket(tt.flag, tt.value), "bucket(%q, %v)", tt.flag, tt.value)
	}
}
