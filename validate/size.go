package validate

import (
	"fmt"

	ffErrors "github.com/luckylanggam/tools-go-featureflags/errors"
)

const (
	// MaxDocumentBytes is the AppConfig hosted configuration and deployment
	// size quota (2,048 KB by default; raising it needs an AWS approval).
	MaxDocumentBytes = 2048 * 1024

	// WarnDocumentPercent is how full the quota may get before CheckSize
	// warns, leaving room to react before saves start failing.
	WarnDocumentPercent = 75
)

// CheckSize checks a document as it will be stored. It returns an error over
// MaxDocumentBytes, which AppConfig would reject, and a warning over
// WarnDocumentPercent of it.
func CheckSize(raw []byte) (warning string, err error) {
	n := len(raw)
	if n > MaxDocumentBytes {
		return "", fmt.Errorf(ffErrors.ErrValidateDocumentTooLarge, n, MaxDocumentBytes)
	}
	if n > MaxDocumentBytes*WarnDocumentPercent/100 {
		return fmt.Sprintf(ffErrors.WarnValidateDocumentSize, n, WarnDocumentPercent, MaxDocumentBytes), nil
	}
	return "", nil
}
