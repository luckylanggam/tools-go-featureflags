package validate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValueKey_KeepsLargeIDsApart(t *testing.T) {
	assert.NotEqual(t, valueKey(json.Number("9007199254740993.0")), valueKey(json.Number("9007199254740992")))
	assert.Equal(t, valueKey(json.Number("9007199254740993.0")), valueKey(json.Number("9007199254740993")))
}
