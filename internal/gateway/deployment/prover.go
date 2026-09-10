// Package deployment owns process-local deployment readiness proofs. Proof
// values are derived from the running executable, catalog and configured key
// material; administrative callers cannot manufacture them.
package deployment

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/security"
)

const ProofTTL = 5 * time.Minute

type Prover struct {
	store    *repository.Store
	identity repository.DeploymentIdentity
}

type Proof struct {
	GenerationID uint64
	MemberID     uint64
	ReleaseID    uint64
	ExpiresAt    time.Time
	Identity     repository.DeploymentIdentity
}

type keyVersion struct {
	keyringID   uint64
	version     uint32
	purpose     string
	state       string
	providerRef string
}

func NewProver(store *repository.Store, identity repository.DeploymentIdentity) (*Prover, error) {
	if store == nil || identity.Validate() != nil {
		return nil, repository.ErrInvalidInput
	}
	return &Prover{store: store, identity: identity}, nil
}

// Prove registers the current process in a preparing generation and records
// catalog plus cryptographic readiness after executing all required checks.
// A zero releaseID selects the active release, or the latest compatible
// published release during first-time setup.
func (p *Prover) Prove(ctx context.Context, generationID, releaseID uint64) (Proof, error) {
	if ctx == nil || generationID == 0 {
		return Proof{}, repository.ErrInvalidInput
	}
	proof := Proof{GenerationID: generationID, Identity: p.identity}
	err := p.store.WithTx(ctx, func(tx *sql.Tx) error {
		var generationStatus, generationSemantic string
		if err := tx.QueryRowContext(ctx, `SELECT status,semantic_digest FROM gw_deployment_generations WHERE id=? FOR UPDATE`, generationID).Scan(&generationStatus, &generationSemantic); errors.Is(err, sql.ErrNoRows) {
			return repository.ErrNotFound
		} else if err != nil {
			return err
		}
		if generationStatus != "preparing" && generationStatus != "active" || generationSemantic != adapter.SemanticDigest() {
			return repository.ErrConflict
		}

		memberID, err := p.memberID(ctx, tx, generationID)
		if errors.Is(err, sql.ErrNoRows) {
			if generationStatus != "preparing" {
				return repository.ErrConflict
			}
			memberID, err = p.store.AddDeploymentMember(ctx, tx, generationID, p.identity.InstanceID, p.identity.Role)
		}
		if err != nil {
			return err
		}
		proof.MemberID = memberID

		if generationStatus == "active" {
			var matching uint64
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_catalog_readiness WHERE deployment_generation_id=? AND deployment_member_id=? AND adapter_digest=?`, generationID, memberID, p.identity.AdapterDigest).Scan(&matching); err != nil {
				return err
			}
			if matching == 0 {
				return repository.ErrConflict
			}
		}

		selectedRelease, contentHash, semanticDigest, err := p.release(ctx, tx, releaseID, generationSemantic)
		if err != nil {
			return err
		}
		proof.ReleaseID = selectedRelease
		if err := verifyCatalogAdapters(ctx, tx, selectedRelease); err != nil {
			return err
		}
		keys, err := loadKeyVersions(ctx, tx)
		if err != nil {
			return err
		}
		if err := probeKeyVersions(keys); err != nil {
			return err
		}

		proof.ExpiresAt = time.Now().UTC().Add(ProofTTL)
		if err := p.store.RecordCatalogReadiness(ctx, tx, generationID, memberID, selectedRelease, contentHash, semanticDigest, p.identity.AdapterDigest, "ready", proof.ExpiresAt); err != nil {
			return err
		}
		for _, key := range keys {
			for _, operation := range requiredOperations(key.state) {
				if err := p.store.RecordCryptoReadiness(ctx, tx, generationID, memberID, key.keyringID, key.version, operation, "ready", proof.ExpiresAt); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Proof{}, fmt.Errorf("prove deployment readiness: %w", err)
	}
	return proof, nil
}

// RefreshActive renews an existing proof for the exact same executable. It
// never adds a new member to a frozen generation or adopts an old generation
// after the executable has changed.
func (p *Prover) RefreshActive(ctx context.Context) (Proof, error) {
	if ctx == nil {
		return Proof{}, repository.ErrInvalidInput
	}
	var generationID, releaseID sql.NullInt64
	err := p.store.DB().QueryRowContext(ctx, `SELECT active_deployment_generation_id,active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&generationID, &releaseID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (!releaseID.Valid || releaseID.Int64 <= 0) {
		return Proof{}, repository.ErrNotFound
	}
	if err != nil || !generationID.Valid || generationID.Int64 <= 0 {
		if err == nil {
			err = repository.ErrNotFound
		}
		return Proof{}, err
	}
	return p.Prove(ctx, uint64(generationID.Int64), uint64(releaseID.Int64))
}

func (p *Prover) memberID(ctx context.Context, tx *sql.Tx, generationID uint64) (uint64, error) {
	var memberID uint64
	err := tx.QueryRowContext(ctx, `SELECT id FROM gw_deployment_members WHERE deployment_generation_id=? AND instance_id=? AND role=?`, generationID, p.identity.InstanceID, p.identity.Role).Scan(&memberID)
	return memberID, err
}

func (p *Prover) release(ctx context.Context, tx *sql.Tx, releaseID uint64, generationSemantic string) (uint64, string, string, error) {
	if releaseID == 0 {
		var active sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT active_release_id FROM gw_catalog_runtime_state WHERE id=1`).Scan(&active); err != nil {
			return 0, "", "", err
		}
		if active.Valid && active.Int64 > 0 {
			releaseID = uint64(active.Int64)
		} else if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_catalog_releases WHERE status='published' AND semantic_digest=? ORDER BY release_no DESC LIMIT 1`, generationSemantic).Scan(&releaseID); errors.Is(err, sql.ErrNoRows) {
			return 0, "", "", repository.ErrNotFound
		} else if err != nil {
			return 0, "", "", err
		}
	}
	var status, contentHash, semanticDigest string
	if err := tx.QueryRowContext(ctx, `SELECT status,content_hash,semantic_digest FROM gw_catalog_releases WHERE id=? FOR SHARE`, releaseID).Scan(&status, &contentHash, &semanticDigest); errors.Is(err, sql.ErrNoRows) {
		return 0, "", "", repository.ErrNotFound
	} else if err != nil {
		return 0, "", "", err
	}
	if status != "published" || semanticDigest != generationSemantic || semanticDigest != adapter.SemanticDigest() {
		return 0, "", "", repository.ErrConflict
	}
	return releaseID, contentHash, semanticDigest, nil
}

func verifyCatalogAdapters(ctx context.Context, tx *sql.Tx, releaseID uint64) error {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT a.adapter_code,a.contract_version,a.implementation_digest,a.minimum_semantic_version
FROM gw_channel_transports t JOIN gw_adapter_implementations a ON a.id=t.adapter_implementation_id
WHERE t.release_id=? ORDER BY a.adapter_code,a.contract_version`, releaseID)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var code, digest, minimum string
		var version uint32
		if err := rows.Scan(&code, &version, &digest, &minimum); err != nil {
			return err
		}
		descriptor, ok := adapter.DescriptorFor(code, version)
		if !ok || descriptor.ImplementationDigest != digest || descriptor.MinimumSemanticVersion != minimum {
			return repository.ErrConflict
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count == 0 {
		return repository.ErrConflict
	}
	return nil
}

func loadKeyVersions(ctx context.Context, tx *sql.Tx) ([]keyVersion, error) {
	rows, err := tx.QueryContext(ctx, `SELECT k.id,k.purpose,v.key_version,v.status,v.provider_key_ref
FROM crypto_keyring_state k JOIN crypto_key_versions v ON v.keyring_id=k.id
WHERE k.purpose IN ('gateway-credential','gateway-payload')
AND ((v.key_version=k.current_version AND v.status='current') OR v.status='readable')
ORDER BY k.id,v.key_version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []keyVersion
	for rows.Next() {
		var item keyVersion
		if err := rows.Scan(&item.keyringID, &item.purpose, &item.version, &item.state, &item.providerRef); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	current := map[string]int{"gateway-credential": 0, "gateway-payload": 0}
	for _, item := range result {
		if item.state == "current" {
			current[item.purpose]++
		}
	}
	if current["gateway-credential"] != 1 || current["gateway-payload"] != 1 {
		return nil, repository.ErrConflict
	}
	return result, nil
}

func probeKeyVersions(keys []keyVersion) error {
	for _, key := range keys {
		kekName, ok := strings.CutPrefix(key.providerRef, "env:")
		if !ok || strings.TrimSpace(kekName) == "" {
			return repository.ErrConflict
		}
		hmacName := "PRISM_GATEWAY_HMAC_B64"
		if key.purpose == "gateway-payload" {
			hmacName = "PRISM_GATEWAY_PAYLOAD_HMAC_B64"
		} else if key.purpose != "gateway-credential" {
			return repository.ErrConflict
		}
		kek, err := security.DecodeBase64Key(os.Getenv(kekName))
		if err != nil {
			return fmt.Errorf("probe %s key version %d: %w", key.purpose, key.version, err)
		}
		hmacKey, err := security.LoadVersionedHMACKey(hmacName, key.version)
		if err != nil {
			clear(kek)
			return fmt.Errorf("probe %s MAC key version %d: %w", key.purpose, key.version, err)
		}
		if err := probeKeyMaterial(kek, hmacKey, key.version); err != nil {
			clear(kek)
			clear(hmacKey)
			return fmt.Errorf("probe %s key version %d: %w", key.purpose, key.version, err)
		}
		clear(kek)
		clear(hmacKey)
	}
	return nil
}

func probeKeyMaterial(kek, hmacKey []byte, version uint32) error {
	const plaintext = "prism-readiness-v1"
	aad := []byte("prism-readiness-aad-v1")
	digest := security.HMACSHA256(hmacKey, []byte(plaintext))
	if digest == ([32]byte{}) {
		return security.ErrAuthentication
	}
	envelope, err := security.Seal([]byte(plaintext), aad, kek, version)
	if err != nil {
		return err
	}
	opened, err := envelope.Open(aad, kek)
	if err != nil {
		return err
	}
	if !bytes.Equal(opened, []byte(plaintext)) {
		return security.ErrAuthentication
	}
	return nil
}

func requiredOperations(state string) []string {
	if state == "current" {
		return []string{"mac", "wrap", "unwrap", "encrypt", "decrypt"}
	}
	return []string{"mac", "unwrap", "decrypt"}
}
