package runtime

import (
	"errors"
	"testing"
	"time"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestNormalizeCapabilityRecoveryPolicy(t *testing.T) {
	policy, err := normalizeCapabilityRecoveryPolicy(CapabilityRecoveryPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if policy.Interval != defaultCapabilityRecoveryInterval ||
		policy.Grace != defaultCapabilityRecoveryGrace ||
		policy.BatchSize != defaultCapabilityRecoveryBatch {
		t.Fatalf("defaults = %#v", policy)
	}
	for _, invalid := range []CapabilityRecoveryPolicy{
		{Interval: time.Millisecond, Grace: time.Minute, BatchSize: 1},
		{Interval: time.Second, Grace: time.Second, BatchSize: 1},
		{Interval: time.Second, Grace: time.Minute, BatchSize: 1001},
	} {
		if _, err := normalizeCapabilityRecoveryPolicy(invalid); !errors.Is(err, repository.ErrInvalidInput) {
			t.Fatalf("invalid policy %#v returned %v", invalid, err)
		}
	}
}
