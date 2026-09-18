package admin

import (
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

// The catalog options endpoint only ever exposed Descriptor — five fields that
// say which code is deployed and nothing about what that code accepts. Every
// question an operator actually has before filling in a SKU (which upstream
// models does this variant cover? which parameters exist and what are their
// ranges? which billing identifiers may a price expression reference?) lives in
// Manifest, which had zero references under internal/api. These endpoints
// publish it.
//
// Manifest's Go structs carry no json tags, so marshalling them directly would
// expose Go field names as the wire contract and freeze every future rename.
// The DTOs below are the stable shape.

type adapterParamHintDTO struct {
	Kind     string               `json:"kind"`
	Options  []string             `json:"options,omitempty"`
	Min      string               `json:"min,omitempty"`
	Max      string               `json:"max,omitempty"`
	Default  string               `json:"default,omitempty"`
	Allowed  *bool                `json:"allowed,omitempty"`
	Required bool                 `json:"required"`
	Cases    []adapterHintCaseDTO `json:"cases,omitempty"`
}

type adapterHintCaseDTO struct {
	Value     string `json:"value"`
	Dependent string `json:"dependent"`
	Min       string `json:"min,omitempty"`
	Max       string `json:"max,omitempty"`
}

type adapterBillingVarDTO struct {
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Layer  string `json:"layer"`
	From   string `json:"from,omitempty"`
	Domain *struct {
		Kind    string   `json:"kind"`
		Options []string `json:"options,omitempty"`
		Numbers []string `json:"numbers,omitempty"`
		Min     string   `json:"min,omitempty"`
		Max     string   `json:"max,omitempty"`
	} `json:"domain,omitempty"`
}

type adapterRefMediaDTO struct {
	Field        string `json:"field"`
	Max          int    `json:"max"`
	ItemMin      int    `json:"item_seconds_min"`
	ItemMax      int    `json:"item_seconds_max"`
	TotalSeconds int    `json:"total_seconds"`
}

type adapterTierDTO struct {
	UpTo  string `json:"up_to"`
	Value string `json:"value"`
}

type adapterVariantDTO struct {
	Code            string                         `json:"code"`
	Display         string                         `json:"display"`
	Models          []string                       `json:"models"`
	ParamHints      map[string]adapterParamHintDTO `json:"param_hints"`
	RefMedia        []adapterRefMediaDTO           `json:"ref_media,omitempty"`
	HardConstraints []struct {
		Field  string `json:"field"`
		MustBe string `json:"must_be"`
	} `json:"hard_constraints,omitempty"`
	PricingDefaultMode string                      `json:"pricing_default_mode,omitempty"`
	PricingExample     string                      `json:"pricing_example,omitempty"`
	Tiers              map[string][]adapterTierDTO `json:"tiers,omitempty"`
}

type adapterManifestDTO struct {
	Adapter         string                 `json:"adapter"`
	AdapterVersion  uint32                 `json:"adapter_version"`
	Protocol        string                 `json:"protocol"`
	Capability      string                 `json:"capability"`
	DownstreamPaths []string               `json:"downstream_paths"`
	BillingVars     []adapterBillingVarDTO `json:"billing_vars"`
	BillingFuncs    []string               `json:"billing_funcs"`
	Variants        []adapterVariantDTO    `json:"variants"`
}

func adapterManifestPayload(manifest adapter.Manifest) adapterManifestDTO {
	out := adapterManifestDTO{
		Adapter: manifest.Adapter, AdapterVersion: manifest.AdapterVersion,
		Capability: manifest.Capability, DownstreamPaths: manifest.DownstreamPaths,
		BillingFuncs: manifest.BillingFuncs,
		BillingVars:  make([]adapterBillingVarDTO, 0, 8),
		Variants:     make([]adapterVariantDTO, 0, len(manifest.Variants)),
	}
	if descriptor, ok := adapter.DescriptorFor(manifest.Adapter, manifest.AdapterVersion); ok {
		out.Protocol = descriptor.Protocol
	}
	// Flatten the three declaration layers into one list that keeps the layer as
	// a field. A map keyed by layer would force the console to know the layer
	// names before it can render anything. The layer type itself is unexported,
	// so the constants index the map and the wire name sits beside them.
	for _, layer := range []struct {
		name string
		vars []adapter.BillingVar
	}{
		{"common", manifest.BillingVars[adapter.LayerCommon]},
		{"capability", manifest.BillingVars[adapter.LayerCapability]},
		{"adapter", manifest.BillingVars[adapter.LayerAdapter]},
	} {
		for _, v := range layer.vars {
			item := adapterBillingVarDTO{Name: v.Name, Desc: v.Desc, Layer: layer.name, From: v.From}
			if v.Domain != nil {
				item.Domain = &struct {
					Kind    string   `json:"kind"`
					Options []string `json:"options,omitempty"`
					Numbers []string `json:"numbers,omitempty"`
					Min     string   `json:"min,omitempty"`
					Max     string   `json:"max,omitempty"`
				}{Kind: string(v.Domain.Kind), Options: v.Domain.Options, Numbers: v.Domain.Numbers, Min: v.Domain.Min, Max: v.Domain.Max}
			}
			out.BillingVars = append(out.BillingVars, item)
		}
	}
	for _, variant := range manifest.Variants {
		item := adapterVariantDTO{
			Code: variant.Code, Display: variant.Display, Models: variant.Models,
			ParamHints:         make(map[string]adapterParamHintDTO, len(variant.ParamHints)),
			PricingDefaultMode: variant.PricingHints.DefaultMode, PricingExample: variant.PricingHints.Example,
		}
		for name, hint := range variant.ParamHints {
			dto := adapterParamHintDTO{
				Kind: string(hint.Kind), Options: hint.Options, Min: hint.Min, Max: hint.Max,
				Default: hint.Default, Allowed: hint.Allowed, Required: hint.Required,
			}
			for _, c := range hint.Cases {
				dto.Cases = append(dto.Cases, adapterHintCaseDTO{Value: c.Value, Dependent: c.Dependent, Min: c.Min, Max: c.Max})
			}
			item.ParamHints[name] = dto
		}
		// Map iteration order is random and this payload is read by humans
		// comparing two adapters side by side, so sort what the map gave us.
		refFields := make([]string, 0, len(variant.RefMedia))
		for field := range variant.RefMedia {
			refFields = append(refFields, field)
		}
		sort.Strings(refFields)
		for _, field := range refFields {
			limit := variant.RefMedia[field]
			item.RefMedia = append(item.RefMedia, adapterRefMediaDTO{
				Field: field, Max: limit.Max, ItemMin: limit.ItemSeconds[0], ItemMax: limit.ItemSeconds[1], TotalSeconds: limit.TotalSeconds,
			})
		}
		for _, constraint := range variant.HardConstraints {
			item.HardConstraints = append(item.HardConstraints, struct {
				Field  string `json:"field"`
				MustBe string `json:"must_be"`
			}{Field: constraint.Field, MustBe: constraint.MustBe})
		}
		if len(variant.Tiers) > 0 {
			item.Tiers = make(map[string][]adapterTierDTO, len(variant.Tiers))
			for name, tiers := range variant.Tiers {
				rows := make([]adapterTierDTO, 0, len(tiers))
				for _, tier := range tiers {
					rows = append(rows, adapterTierDTO{UpTo: tier.UpTo, Value: tier.Value})
				}
				item.Tiers[name] = rows
			}
		}
		out.Variants = append(out.Variants, item)
	}
	return out
}

// ListUnifiedAdapters is the roster an onboarding conversation or a product
// dialog starts from: every adapter this binary runs, whether it is selectable
// for a catalog product, and whether a manifest exists to drive form rendering.
func ListUnifiedAdapters(c *gin.Context) {
	registry := adapter.ProcessManifests()
	items := make([]gin.H, 0, 10)
	for _, descriptor := range adapter.Descriptors() {
		_, hasManifest := registry.Manifest(descriptor.Code, descriptor.Version)
		items = append(items, gin.H{
			"code": descriptor.Code, "version": descriptor.Version, "protocol": descriptor.Protocol,
			"minimum_semantic_version": descriptor.MinimumSemanticVersion,
			"implementation_digest":    descriptor.ImplementationDigest,
			"has_manifest":             hasManifest,
			// catalog_discovery adapters back price/model discovery sources and are
			// rejected by the product validator, so say that here rather than
			// letting someone find out from a 400.
			"selectable_for_product": descriptor.Protocol != "catalog_discovery",
		})
	}
	resp.Success(c, gin.H{"items": items, "semantic_digest": adapter.SemanticDigest()})
}

// GetUnifiedAdapterManifest returns one adapter's full declaration. Version
// defaults to 1 because every descriptor in this binary is at version 1 and
// making callers spell it out buys nothing.
func GetUnifiedAdapterManifest(c *gin.Context) {
	code := strings.ToLower(strings.TrimSpace(c.Param("code")))
	version := uint32(1)
	if raw := strings.TrimSpace(c.Query("version")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || parsed == 0 {
			unifiedChannelError(c, repository.ErrInvalidInput)
			return
		}
		version = uint32(parsed)
	}
	descriptor, found := adapter.DescriptorFor(code, version)
	if !found {
		unifiedChannelError(c, repository.ErrNotFound)
		return
	}
	manifest, ok := adapter.ProcessManifests().Manifest(code, version)
	if !ok {
		// A descriptor without a manifest is a supported adapter that simply
		// cannot be priced by expression yet. Answer with the descriptor instead
		// of 404, so the console can say which one it is.
		resp.Success(c, gin.H{
			"adapter": descriptor.Code, "adapter_version": descriptor.Version, "protocol": descriptor.Protocol,
			"has_manifest": false,
		})
		return
	}
	payload := adapterManifestPayload(manifest)
	resp.Success(c, gin.H{
		"adapter": payload.Adapter, "adapter_version": payload.AdapterVersion, "protocol": payload.Protocol,
		"has_manifest": true, "capability": payload.Capability, "downstream_paths": payload.DownstreamPaths,
		"billing_vars": payload.BillingVars, "billing_funcs": payload.BillingFuncs, "variants": payload.Variants,
		"minimum_semantic_version": descriptor.MinimumSemanticVersion,
		"implementation_digest":    descriptor.ImplementationDigest,
	})
}
