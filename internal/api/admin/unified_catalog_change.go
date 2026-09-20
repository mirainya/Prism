package admin

import (
	"database/sql"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// ChangeUnifiedSellRate updates the active catalog in one transaction. The
// expected configuration version prevents concurrent edits from overwriting a
// newer value, and the next request observes the new rate immediately.
func ChangeUnifiedSellRate(c *gin.Context) {
	var in repository.SellRateChange
	if !unifiedChannelBody(c, &in) {
		return
	}
	// Taken from the running binary rather than the request, for the same reason
	// CreateUnifiedCatalogDraft does it: the digest describes which adapters this
	// executable has, and a caller cannot know that better than the process.
	in.SemanticDigest = adapter.SemanticDigest()
	// Refused before the transaction opens so bad input never takes the runtime
	// singleton's lock, which every other B-class change also has to queue on.
	in.Normalize()
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.ChangeSellRate(c.Request.Context(), tx, in, rateEvidenceHMAC, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}

// ChangeUnifiedCostRate updates the active upstream cost in the same transaction
// model as ChangeUnifiedSellRate.
func ChangeUnifiedCostRate(c *gin.Context) {
	var in repository.CostRateChange
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticDigest = adapter.SemanticDigest()
	in.Normalize()
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.ChangeCostRate(c.Request.Context(), tx, in, rateEvidenceHMAC, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}

// ChangeUnifiedRouteWeight updates the active route by its stable business
// identity, without creating a release snapshot.
func ChangeUnifiedRouteWeight(c *gin.Context) {
	var in repository.RouteWeightChange
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticDigest = adapter.SemanticDigest()
	in.Normalize()
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.ChangeRouteWeight(c.Request.Context(), tx, in, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}

// ChangeUnifiedSKUVariant switches the manifest commercial shape used by one
// active SKU after validating it against the reachable adapters.
func ChangeUnifiedSKUVariant(c *gin.Context) {
	var in repository.SKUVariantChange
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticDigest = adapter.SemanticDigest()
	in.Normalize()
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.ChangeSKUVariant(c.Request.Context(), tx, in, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}

// ChangeUnifiedSKUDownstreamPaths replaces a SKU's complete downstream path set
// in one transaction, so a partial mapping is never observable.
func ChangeUnifiedSKUDownstreamPaths(c *gin.Context) {
	var in repository.SKUDownstreamPathsChange
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticDigest = adapter.SemanticDigest()
	in.Normalize()
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.ChangeSKUDownstreamPaths(c.Request.Context(), tx, in, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}

type activeCatalogProductCreateRequest struct {
	repository.CatalogProductInput
	ExpectedActiveReleaseID uint64          `json:"expected_active_release_id"`
	ExpectedConfigVersion   uint64          `json:"expected_config_version"`
	DraftExpectedVersion    json.RawMessage `json:"expected_version"`
}

// CreateUnifiedActiveCatalogProduct adds a complete upstream product to the
// active catalog without creating or publishing a release snapshot.
func CreateUnifiedActiveCatalogProduct(c *gin.Context) {
	var request activeCatalogProductCreateRequest
	if !unifiedChannelBody(c, &request) {
		return
	}
	if request.ExpectedActiveReleaseID == 0 || request.ExpectedConfigVersion == 0 || len(request.DraftExpectedVersion) != 0 {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	in := request.CatalogProductInput
	in.ExpectedVersion = request.ExpectedConfigVersion
	if err := in.Normalize(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	descriptor, found := adapter.DescriptorFor(in.AdapterCode, in.AdapterVersion)
	if !found {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	in.Protocol = descriptor.Protocol
	in.Adapter = repository.CatalogAdapterInput{Code: descriptor.Code, Version: descriptor.Version, Protocol: descriptor.Protocol, ImplementationDigest: descriptor.ImplementationDigest, MinimumSemanticVersion: descriptor.MinimumSemanticVersion}
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if err := adapter.ValidateCatalogProduct(descriptor.Code, descriptor.Version, in.BaseURL, in.RequestMethod, in.RequestPath, in.VendorModel, in.TaskScope, in.CapabilityConstraints); err != nil {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.CreateActiveCatalogProduct(c.Request.Context(), tx, request.ExpectedActiveReleaseID, request.ExpectedConfigVersion, in, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}

// ChangeUnifiedProduct edits the upstream vendor model and capability
// constraint document while keeping transport and credential bindings intact.
func ChangeUnifiedProduct(c *gin.Context) {
	var in repository.ProductChange
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticDigest = adapter.SemanticDigest()
	if err := in.Normalize(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	var result repository.CatalogChangeResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.ChangeProduct(c.Request.Context(), tx, in, func(code string, version uint32, baseURL, method, path, vendorModel, taskScope string, config []byte) error {
			return adapter.ValidateCatalogProduct(code, version, baseURL, method, path, vendorModel, taskScope, config)
		}, actor)
		return err
	}) {
		return
	}
	respondCatalogChange(c, result)
}

func respondCatalogChange(c *gin.Context, result repository.CatalogChangeResult) {
	resp.Success(c, gin.H{
		"release_id":        result.ReleaseID,
		"config_version":    result.ConfigVersion,
		"activated":         true,
		"source_release_id": result.SourceReleaseID,
	})
}

// RollbackUnifiedCatalogRelease is retained only as a compatibility symbol for
// older in-process callers. Historical rollback is no longer exposed by the
// admin routes; active configuration is edited directly.
func RollbackUnifiedCatalogRelease(c *gin.Context) {
	if _, err := resp.ParseUintParam(c, "id"); err != nil {
		return
	}
	var in repository.CatalogRollbackInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.SemanticDigest = adapter.SemanticDigest()
	in.Normalize()
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if !unifiedGatewayWrite(c, func(_ *repository.Store, _ *sql.Tx, _ uint64) error {
		return repository.ErrConflict
	}) {
		return
	}
	unifiedChannelError(c, repository.ErrConflict)
}
