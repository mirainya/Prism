package admin

import (
	"database/sql"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// OnboardUnifiedCatalogModel creates a complete, immediately active model graph
// in one transaction. Adapter identity comes from the running binary rather
// than from caller-controlled protocol or digest fields.
func OnboardUnifiedCatalogModel(c *gin.Context) {
	var in repository.CatalogModelOnboardInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	if err := in.Normalize(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	descriptor, found := adapter.DescriptorFor(in.Product.AdapterCode, in.Product.AdapterVersion)
	if !found {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	in.Product.Protocol = descriptor.Protocol
	in.Product.Adapter = repository.CatalogAdapterInput{
		Code: descriptor.Code, Version: descriptor.Version, Protocol: descriptor.Protocol,
		ImplementationDigest: descriptor.ImplementationDigest, MinimumSemanticVersion: descriptor.MinimumSemanticVersion,
	}
	if err := in.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if err := adapter.ValidateCatalogProduct(descriptor.Code, descriptor.Version, in.Product.BaseURL, in.Product.RequestMethod, in.Product.RequestPath, in.Product.VendorModel, in.Product.TaskScope, in.Product.CapabilityConstraints); err != nil {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	if err := adapter.ValidateDownstreamPath(descriptor.Code, descriptor.Version, in.SKU.RouteTemplate); err != nil {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}

	var result repository.CatalogModelOnboardResult
	if !unifiedGatewayWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) error {
		var err error
		result, err = store.OnboardCatalogModel(c.Request.Context(), tx, in, rateEvidenceHMAC, actor)
		return err
	}) {
		return
	}
	resp.Success(c, gin.H{
		"release_id": result.ReleaseID, "config_version": result.ConfigVersion,
		"activated": true, "source_release_id": result.SourceReleaseID,
		"sku_id": result.SKUID, "product_id": result.ProductID,
		"offering_id": result.OfferingID, "cost_plan_id": result.CostPlanID,
		"sell_rate_id": result.SellRateID, "cost_rate_id": result.CostRateID,
	})
}
