package admin

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	deploymentproof "github.com/mirainya/Prism/internal/gateway/deployment"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

type deploymentGenerationRequest struct {
	GenerationNo    uint64 `json:"generation_no"`
	SemanticVersion string `json:"semantic_version"`
}
type deploymentMemberRequest struct {
	InstanceID string `json:"instance_id"`
	Role       string `json:"role"`
}
type catalogReadinessRequest struct {
	ReleaseID      uint64    `json:"release_id"`
	ContentHash    string    `json:"content_hash"`
	SemanticDigest string    `json:"semantic_digest"`
	AdapterDigest  string    `json:"adapter_digest"`
	Status         string    `json:"status"`
	ExpiresAt      time.Time `json:"expires_at"`
}
type cryptoReadinessRequest struct {
	KeyringID  uint64    `json:"keyring_id"`
	KeyVersion uint32    `json:"key_version"`
	Operation  string    `json:"operation"`
	Status     string    `json:"status"`
	ExpiresAt  time.Time `json:"expires_at"`
}
type activateCatalogRequest struct {
	GenerationID            uint64 `json:"generation_id"`
	ExpectedStateVersion    uint64 `json:"expected_state_version"`
	ExpectedActiveReleaseID uint64 `json:"expected_active_release_id"`
}

type proveCurrentDeploymentRequest struct {
	ReleaseID uint64 `json:"release_id"`
}

func deploymentStore() (*repository.Store, error) {
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	return repository.New(db)
}

func CreateUnifiedDeployment(c *gin.Context) {
	var in deploymentGenerationRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	in.SemanticVersion = strings.TrimSpace(in.SemanticVersion)
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	var id uint64
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var e error
		id, e = store.CreateDeploymentGeneration(c.Request.Context(), tx, repository.DeploymentGenerationInput{GenerationNo: in.GenerationNo, SemanticVersion: in.SemanticVersion, SemanticDigest: adapter.SemanticDigest()})
		return e
	})
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"id": id, "status": "preparing"})
}

func AddUnifiedDeploymentMember(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in deploymentMemberRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if in.InstanceID == "" {
		in.InstanceID = identity.InstanceID
	}
	if in.Role == "" {
		in.Role = identity.Role
	}
	if in.InstanceID != identity.InstanceID || in.Role != identity.Role {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "deployment member must identify the current process"))
		return
	}
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	var member uint64
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var e error
		member, e = store.AddDeploymentMember(c.Request.Context(), tx, uint64(id), in.InstanceID, in.Role)
		return e
	})
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"id": member})
}

func RecordUnifiedCatalogReadiness(c *gin.Context) {
	gen, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	member, err := resp.ParseUintParam(c, "member_id")
	if err != nil {
		return
	}
	var in catalogReadinessRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if in.Status == "ready" {
		if err := ensureCurrentDeploymentMember(c.Request.Context(), uint64(gen), uint64(member), identity); err != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
			return
		}
		proof, proofErr := proveCurrentDeployment(c.Request.Context(), uint64(gen), in.ReleaseID, identity)
		if proofErr != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, proofErr.Error()))
			return
		}
		if proof.MemberID != uint64(member) {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "deployment member is not the current process"))
			return
		}
		resp.Success(c, gin.H{"ready": true, "expires_at": proof.ExpiresAt})
		return
	}
	if in.AdapterDigest != identity.AdapterDigest || in.SemanticDigest != adapter.SemanticDigest() {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "readiness digest does not match the current process"))
		return
	}
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		if err := requireCurrentDeploymentMember(c.Request.Context(), tx, uint64(gen), uint64(member), identity); err != nil {
			return err
		}
		return store.RecordCatalogReadiness(c.Request.Context(), tx, uint64(gen), uint64(member), in.ReleaseID, in.ContentHash, in.SemanticDigest, in.AdapterDigest, in.Status, in.ExpiresAt)
	})
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"ready": in.Status == "ready"})
}

func RecordUnifiedCryptoReadiness(c *gin.Context) {
	gen, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	member, err := resp.ParseUintParam(c, "member_id")
	if err != nil {
		return
	}
	var in cryptoReadinessRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	if in.Status == "ready" {
		if err := ensureCurrentDeploymentMember(c.Request.Context(), uint64(gen), uint64(member), identity); err != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
			return
		}
		proof, proofErr := proveCurrentDeployment(c.Request.Context(), uint64(gen), 0, identity)
		if proofErr != nil {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, proofErr.Error()))
			return
		}
		if proof.MemberID != uint64(member) {
			resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "deployment member is not the current process"))
			return
		}
		resp.Success(c, gin.H{"ready": true, "expires_at": proof.ExpiresAt})
		return
	}
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		if err := requireCurrentDeploymentMember(c.Request.Context(), tx, uint64(gen), uint64(member), identity); err != nil {
			return err
		}
		return store.RecordCryptoReadiness(c.Request.Context(), tx, uint64(gen), uint64(member), in.KeyringID, in.KeyVersion, in.Operation, in.Status, in.ExpiresAt)
	})
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"ready": in.Status == "ready"})
}

func ActivateUnifiedDeployment(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		return store.ActivateDeploymentGeneration(c.Request.Context(), tx, uint64(id), identity)
	})
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"active": true})
}

// ActivateUnifiedCatalog moves the runtime pointer only after every member of
// the selected deployment proves the exact published release is ready.
func ActivateUnifiedCatalog(c *gin.Context) {
	releaseID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in activateCatalogRequest
	if err := c.ShouldBindJSON(&in); err != nil || in.GenerationID == 0 || in.ExpectedStateVersion == 0 {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, "generation_id and expected_state_version are required"))
		return
	}
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		if in.ExpectedActiveReleaseID != 0 {
			return store.ActivateReleaseWhenReadyFrom(c.Request.Context(), tx, uint64(releaseID), in.ExpectedActiveReleaseID, in.ExpectedStateVersion, in.GenerationID, identity)
		}
		return store.ActivateReleaseWhenReady(c.Request.Context(), tx, uint64(releaseID), in.ExpectedStateVersion, in.GenerationID, identity)
	})
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"active": true, "release_id": releaseID})
}

func ProveCurrentUnifiedDeployment(c *gin.Context) {
	generationID, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in proveCurrentDeploymentRequest
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	proof, err := proveCurrentDeployment(c.Request.Context(), uint64(generationID), in.ReleaseID, identity)
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{
		"generation_id":  proof.GenerationID,
		"member_id":      proof.MemberID,
		"release_id":     proof.ReleaseID,
		"instance_id":    proof.Identity.InstanceID,
		"role":           proof.Identity.Role,
		"adapter_digest": proof.Identity.AdapterDigest,
		"expires_at":     proof.ExpiresAt,
	})
}

func UnifiedDeploymentIdentity(c *gin.Context) {
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	resp.Success(c, gin.H{
		"instance_id": identity.InstanceID, "role": identity.Role,
		"adapter_digest": identity.AdapterDigest, "semantic_digest": adapter.SemanticDigest(),
	})
}

func proveCurrentDeployment(ctx context.Context, generationID, releaseID uint64, identity repository.DeploymentIdentity) (deploymentproof.Proof, error) {
	store, err := deploymentStore()
	if err != nil {
		return deploymentproof.Proof{}, err
	}
	prover, err := deploymentproof.NewProver(store, identity)
	if err != nil {
		return deploymentproof.Proof{}, err
	}
	return prover.Prove(ctx, generationID, releaseID)
}

func requireCurrentDeploymentMember(ctx context.Context, tx *sql.Tx, generationID, memberID uint64, identity repository.DeploymentIdentity) error {
	var count uint64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members WHERE id=? AND deployment_generation_id=? AND instance_id=? AND role=?`, memberID, generationID, identity.InstanceID, identity.Role).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return repository.ErrConflict
	}
	return nil
}

func ensureCurrentDeploymentMember(ctx context.Context, generationID, memberID uint64, identity repository.DeploymentIdentity) error {
	store, err := deploymentStore()
	if err != nil {
		return err
	}
	var count uint64
	if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_deployment_members WHERE id=? AND deployment_generation_id=? AND instance_id=? AND role=?`, memberID, generationID, identity.InstanceID, identity.Role).Scan(&count); err != nil {
		return err
	}
	if count != 1 {
		return repository.ErrConflict
	}
	return nil
}
