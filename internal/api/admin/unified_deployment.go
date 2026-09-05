package admin

import (
	"database/sql"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	pkgErrors "github.com/mirainya/Prism/pkg/errors"
)

type deploymentGenerationRequest struct {
	GenerationNo    uint64 `json:"generation_no"`
	SemanticVersion string `json:"semantic_version"`
	SemanticDigest  string `json:"semantic_digest"`
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
	GenerationID         uint64 `json:"generation_id"`
	ExpectedStateVersion uint64 `json:"expected_state_version"`
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
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	var id uint64
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		var e error
		id, e = store.CreateDeploymentGeneration(c.Request.Context(), tx, repository.DeploymentGenerationInput{GenerationNo: in.GenerationNo, SemanticVersion: in.SemanticVersion, SemanticDigest: in.SemanticDigest})
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
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
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
	store, err := deploymentStore()
	if err != nil {
		resp.InternalError(c, pkgErrors.ErrInternalError)
		return
	}
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
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
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error { return store.ActivateDeploymentGeneration(c.Request.Context(), tx, uint64(id)) })
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
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error {
		return store.ActivateReleaseWhenReady(c.Request.Context(), tx, uint64(releaseID), in.ExpectedStateVersion, in.GenerationID)
	})
	if err != nil {
		resp.BadRequest(c, pkgErrors.WithMessage(pkgErrors.ErrInvalidParams, err.Error()))
		return
	}
	resp.Success(c, gin.H{"active": true, "release_id": releaseID})
}
