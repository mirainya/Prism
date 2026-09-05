package credentials

import "testing"

func TestCredentialStateTransitions(t *testing.T) {
	tests := []struct {
		name string
		fn   func() error
	}{
		{"pool active to draining", func() error { return TransitionPool(PoolActive, PoolDraining) }},
		{"pool draining to disabled", func() error { return TransitionPool(PoolDraining, PoolDisabled) }},
		{"credential active to draining", func() error { return TransitionCredential(CredentialActive, CredentialDraining) }},
		{"grant active to revoked", func() error { return TransitionGrant(GrantActive, GrantRevoked) }},
		{"version preparing to active", func() error { return TransitionVersion(VersionPreparing, VersionActive) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.fn(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCredentialStateTransitionsRejectReactivation(t *testing.T) {
	if err := TransitionPool(PoolDisabled, PoolActive); err == nil {
		t.Fatal("disabled pool was reactivated")
	}
	if err := TransitionCredential(CredentialDraining, CredentialActive); err == nil {
		t.Fatal("draining credential was reactivated")
	}
	if err := TransitionVersion(VersionRetired, VersionActive); err == nil {
		t.Fatal("retired version was reactivated")
	}
}
