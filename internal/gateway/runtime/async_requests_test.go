package runtime

import (
	"testing"

	"github.com/mirainya/Prism/internal/gateway/execution"
)

func TestAsyncRequestAllowedPermitsOnlyQueryForTerminatedUnknown(t *testing.T) {
	if !asyncRequestAllowed(execution.AsyncTerminatedUnknown, "query") {
		t.Fatal("trusted task recovery query was rejected")
	}
	for _, action := range []string{"submit", "recover", "cancel"} {
		if asyncRequestAllowed(execution.AsyncTerminatedUnknown, action) {
			t.Fatalf("terminated unknown execution allowed %q", action)
		}
	}
}
