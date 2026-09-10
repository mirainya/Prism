package responses

import (
	"regexp"
	"testing"
)

func TestNewResponseIDFitsUnifiedResourceIdentity(t *testing.T) {
	id := newResponseID()
	if len(id) != 36 || !regexp.MustCompile(`^resp_[0-9a-f]{31}$`).MatchString(id) {
		t.Fatalf("response id=%q", id)
	}
}
