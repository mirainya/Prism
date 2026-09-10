package runtime

import (
	"strings"
	"testing"
)

func TestCurrentProcessIdentityUsesConfiguredInstanceAndExactExecutableDigest(t *testing.T) {
	t.Setenv(instanceIDEnvironment, "prism-node-1")
	t.Setenv(instanceRoleEnvironment, "API-WORKER")
	identity, err := CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if identity.InstanceID != "prism-node-1" || identity.Role != "api-worker" || len(identity.AdapterDigest) != 64 {
		t.Fatalf("identity = %+v", identity)
	}
	if digest, err := currentExecutableDigest(); err != nil || digest != identity.AdapterDigest {
		t.Fatalf("digest=%q err=%v", digest, err)
	}
}

func TestCurrentProcessIdentityRejectsInvalidConfiguredIdentity(t *testing.T) {
	t.Setenv(instanceIDEnvironment, strings.Repeat("x", 129))
	if _, err := CurrentProcessIdentity(); err == nil {
		t.Fatal("oversized instance id was accepted")
	}
}
