package video

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/mirainya/Prism/pkg/safeurl"
	"gorm.io/gorm"
)

const (
	ResolvedAssetRefURL            = "url"
	ResolvedAssetRefProviderObject = "provider_object"
	AssetResolverDirectURL         = "direct_url"
	AssetResolverPresignedUpload   = "presigned_upload"
)

type ResolvedAsset struct {
	RefType  string
	RefValue string
}

type AssetResolver interface {
	Prepare(context.Context, *VideoAsset) (*ResolvedAsset, error)
}

type DirectURLResolver struct{}

func (DirectURLResolver) Prepare(ctx context.Context, asset *VideoAsset) (*ResolvedAsset, error) {
	if asset == nil || asset.Status != VideoAssetStatusReady || strings.TrimSpace(asset.StoragePath) == "" {
		return nil, ErrAssetNotReady
	}
	if err := safeurl.Validate(ctx, asset.StoragePath); err != nil {
		return nil, fmt.Errorf("%w: unsafe stored URL: %v", ErrInvalidAsset, err)
	}
	return &ResolvedAsset{RefType: ResolvedAssetRefURL, RefValue: asset.StoragePath}, nil
}

type AssetResolverOptions struct {
	Channel   *VideoChannel
	Key       *VideoChannelKey
	RequestID string
	Client    *http.Client
	DB        *gorm.DB
}

type AssetResolverFactory func(AssetResolverOptions) (AssetResolver, error)

type AssetResolverRegistry struct {
	mu        sync.RWMutex
	factories map[string]AssetResolverFactory
}

func NewAssetResolverRegistry() *AssetResolverRegistry {
	return &AssetResolverRegistry{factories: make(map[string]AssetResolverFactory)}
}

func (r *AssetResolverRegistry) Register(kind string, factory AssetResolverFactory) {
	kind = strings.TrimSpace(kind)
	if r == nil || kind == "" || factory == nil {
		return
	}
	r.mu.Lock()
	r.factories[kind] = factory
	r.mu.Unlock()
}

func (r *AssetResolverRegistry) New(kind string, option AssetResolverOptions) (AssetResolver, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = AssetResolverDirectURL
	}
	r.mu.RLock()
	factory := r.factories[kind]
	r.mu.RUnlock()
	if factory == nil {
		return nil, fmt.Errorf("%w: %s", ErrAssetResolver, kind)
	}
	return factory(option)
}

var assetResolvers = func() *AssetResolverRegistry {
	registry := NewAssetResolverRegistry()
	registry.Register(AssetResolverDirectURL, func(AssetResolverOptions) (AssetResolver, error) {
		return DirectURLResolver{}, nil
	})
	registry.Register(AssetResolverPresignedUpload, newPresignedUploadResolver)
	return registry
}()

func RegisterAssetResolver(kind string, factory AssetResolverFactory) {
	assetResolvers.Register(kind, factory)
}

func NewAssetResolver(kind string, options ...AssetResolverOptions) (AssetResolver, error) {
	var option AssetResolverOptions
	if len(options) > 0 {
		option = options[0]
	}
	return assetResolvers.New(kind, option)
}

// ResolveGenerateRequestAssets replaces Prism asset IDs with references accepted by one upstream channel.
func ResolveGenerateRequestAssets(
	ctx context.Context,
	db *gorm.DB,
	channel *VideoChannel,
	key *VideoChannelKey,
	requestID string,
	tokenID uint,
	req *GenerateRequest,
) error {
	if db == nil || channel == nil || key == nil || req == nil {
		return fmt.Errorf("%w: incomplete asset resolution context", ErrAssetResolver)
	}
	resolver, err := NewAssetResolver(channel.AssetResolver, AssetResolverOptions{
		Channel: channel, Key: key, RequestID: requestID, DB: db,
	})
	if err != nil {
		return err
	}
	assets := NewAssetService(db)
	for index := range req.Content {
		if req.Content[index].AssetID == "" {
			continue
		}
		asset, err := assets.GetReady(ctx, tokenID, req.Content[index].AssetID)
		if err != nil {
			return fmt.Errorf("resolve asset: %w", err)
		}
		if req.Content[index].DurationSeconds == 0 && asset.DurationSeconds != nil {
			req.Content[index].DurationSeconds = *asset.DurationSeconds
		}
		resolved, err := resolver.Prepare(ctx, asset)
		if err != nil {
			return fmt.Errorf("prepare asset: %w", err)
		}
		switch resolved.RefType {
		case ResolvedAssetRefURL:
			req.Content[index].URL = resolved.RefValue
		case ResolvedAssetRefProviderObject:
			req.Content[index].StorageObjectID = resolved.RefValue
		default:
			return fmt.Errorf("unsupported resolved asset type %q", resolved.RefType)
		}
		req.Content[index].AssetID = ""
	}
	return nil
}
