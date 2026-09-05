// Package credentials owns credential lifecycle rules. Persistence adapters
// must apply these transitions under a row lock and increment the stored
// state/configuration version in the same transaction.
package credentials

import "fmt"

type PoolState string

const (
	PoolActive   PoolState = "active"
	PoolDraining PoolState = "draining"
	PoolDisabled PoolState = "disabled"
)

type CredentialState string

const (
	CredentialActive   CredentialState = "active"
	CredentialDraining CredentialState = "draining"
	CredentialDisabled CredentialState = "disabled"
)

type GrantState string

type Purpose string

const (
	PurposeExecution        Purpose = "execution"
	PurposeCatalogDiscovery Purpose = "catalog_discovery"
	PurposeCallbackVerify   Purpose = "upstream_callback_verify"
)

const (
	GrantActive   GrantState = "active"
	GrantDraining GrantState = "draining"
	GrantRevoked  GrantState = "revoked"
)

type VersionState string

const (
	VersionPreparing  VersionState = "preparing"
	VersionActive     VersionState = "active"
	VersionSuperseded VersionState = "superseded"
	VersionRetired    VersionState = "retired"
)

func TransitionPool(from, to PoolState) error {
	if from == PoolActive && to == PoolDraining || from == PoolDraining && to == PoolDisabled {
		return nil
	}
	return invalidTransition("credential pool", from, to)
}

func TransitionCredential(from, to CredentialState) error {
	if from == CredentialActive && to == CredentialDraining || from == CredentialDraining && to == CredentialDisabled {
		return nil
	}
	return invalidTransition("credential", from, to)
}

func TransitionGrant(from, to GrantState) error {
	if from == GrantActive && (to == GrantDraining || to == GrantRevoked) || from == GrantDraining && to == GrantRevoked {
		return nil
	}
	return invalidTransition("purpose grant", from, to)
}

func TransitionVersion(from, to VersionState) error {
	if from == VersionPreparing && to == VersionActive || from == VersionActive && to == VersionSuperseded || from == VersionSuperseded && to == VersionRetired {
		return nil
	}
	return invalidTransition("credential version", from, to)
}

func invalidTransition(kind string, from, to any) error {
	return fmt.Errorf("%s transition %v -> %v is not allowed", kind, from, to)
}
