package security

import "fmt"

type KeyVersionState string

const (
	KeyPreparing       KeyVersionState = "preparing"
	KeyReadable        KeyVersionState = "readable"
	KeyCurrent         KeyVersionState = "current"
	KeyRetired         KeyVersionState = "retired"
	KeySecurityRevoked KeyVersionState = "security_revoked"
)

// TransitionKeyVersion enforces the key rotation lifecycle. Security
// revocation is terminal and is allowed from every non-revoked state.
func TransitionKeyVersion(from, to KeyVersionState) error {
	if to == KeySecurityRevoked && from != KeySecurityRevoked {
		return nil
	}
	valid := from == KeyPreparing && (to == KeyReadable || to == KeyRetired) ||
		from == KeyReadable && (to == KeyCurrent || to == KeyRetired) ||
		from == KeyCurrent && to == KeyReadable
	if valid {
		return nil
	}
	return fmt.Errorf("key version transition %q -> %q is not allowed", from, to)
}
