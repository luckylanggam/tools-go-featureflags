package validate_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/luckylanggam/tools-go-featureflags/validate"
)

func TestCheckSize(t *testing.T) {
	warnAt := validate.MaxDocumentBytes * validate.WarnDocumentPercent / 100

	cases := map[string]struct {
		size    int
		warning string
		err     string
	}{
		"small":            {size: 500 * 1024},
		"at the warn line": {size: warnAt},
		"over the warn line": {
			size:    warnAt + 1,
			warning: "document is 1572865 bytes, over 75% of the AppConfig limit of 2097152 bytes",
		},
		"at the limit": {
			size:    validate.MaxDocumentBytes,
			warning: "document is 2097152 bytes, over 75% of the AppConfig limit of 2097152 bytes",
		},
		"over the limit": {
			size: validate.MaxDocumentBytes + 1,
			err:  "document is 2097153 bytes, over the AppConfig limit of 2097152 bytes",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			warning, err := validate.CheckSize(make([]byte, tc.size))
			assert.Equal(t, tc.warning, warning)
			if tc.err == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tc.err)
			}
		})
	}
}
