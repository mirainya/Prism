package transport

import (
	"errors"

	"github.com/mirainya/Prism/internal/gateway/canonical"
)

// DetailsFromError returns structured upstream details preserved by a transport.
func DetailsFromError(err error) *canonical.Error {
	if err == nil {
		return nil
	}
	var provider interface{ ErrorDetails() *canonical.Error }
	if errors.As(err, &provider) {
		return provider.ErrorDetails()
	}
	return nil
}
