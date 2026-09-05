package runtime

import (
	"context"
	"errors"
	"testing"
)

func TestRequireConfiguredReadinessRejectsNilDatabase(t *testing.T) {
	if !errors.Is(RequireConfiguredReadiness(context.Background(), nil), ErrNotReady) {
		t.Fatal("nil database must not be considered ready")
	}
}
