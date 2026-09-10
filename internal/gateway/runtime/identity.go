package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/mirainya/Prism/internal/gateway/repository"
)

const (
	instanceIDEnvironment   = "PRISM_GATEWAY_INSTANCE_ID"
	instanceRoleEnvironment = "PRISM_GATEWAY_INSTANCE_ROLE"
	defaultInstanceRole     = "api-worker"
)

var binaryDigestState struct {
	sync.Once
	value string
	err   error
}

// CurrentProcessIdentity returns the stable deployment identity and exact
// executable digest of this process. Operators should set the instance ID in
// multi-instance environments; a host name is sufficient for a single node.
func CurrentProcessIdentity() (repository.DeploymentIdentity, error) {
	instanceID := strings.TrimSpace(os.Getenv(instanceIDEnvironment))
	if instanceID == "" {
		var err error
		instanceID, err = os.Hostname()
		if err != nil {
			return repository.DeploymentIdentity{}, fmt.Errorf("resolve gateway instance id: %w", err)
		}
		instanceID = strings.TrimSpace(instanceID)
	}
	role := strings.ToLower(strings.TrimSpace(os.Getenv(instanceRoleEnvironment)))
	if role == "" {
		role = defaultInstanceRole
	}
	digest, err := currentExecutableDigest()
	if err != nil {
		return repository.DeploymentIdentity{}, err
	}
	identity := repository.DeploymentIdentity{InstanceID: instanceID, Role: role, AdapterDigest: digest}
	if err := identity.Validate(); err != nil {
		return repository.DeploymentIdentity{}, err
	}
	return identity, nil
}

func currentExecutableDigest() (string, error) {
	binaryDigestState.Do(func() {
		path, err := os.Executable()
		if err != nil {
			binaryDigestState.err = fmt.Errorf("resolve current executable: %w", err)
			return
		}
		file, err := os.Open(path)
		if err != nil {
			binaryDigestState.err = fmt.Errorf("open current executable: %w", err)
			return
		}
		defer file.Close()
		hash := sha256.New()
		if _, err := io.Copy(hash, file); err != nil {
			binaryDigestState.err = fmt.Errorf("hash current executable: %w", err)
			return
		}
		binaryDigestState.value = hex.EncodeToString(hash.Sum(nil))
	})
	return binaryDigestState.value, binaryDigestState.err
}
