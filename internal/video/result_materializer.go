package video

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mirainya/Prism/pkg/filestorage"
)

var transferGenerationResultURL = filestorage.TransferURL
var transferGenerationResultURLTrusted = filestorage.TransferURLTrusted

type resultStorageConfig struct {
	Enabled      bool     `json:"enabled"`
	TrustedHosts []string `json:"trusted_hosts"`
}

func shouldMaterializeGenerationResult(channel *VideoChannel) bool {
	return channel != nil && channel.EffectiveResultStorageEnabled()
}

func resultStorageTrustedHosts(channel *VideoChannel) []string {
	if channel == nil || len(channel.ExtraConfig) == 0 {
		return nil
	}
	var config struct {
		ResultStorage resultStorageConfig `json:"result_storage"`
	}
	if json.Unmarshal(channel.ExtraConfig, &config) != nil {
		return nil
	}
	return config.ResultStorage.TrustedHosts
}

// MaterializeGenerationResult copies a provider result into Prism storage when
// file storage is configured. Providers with short-lived result URLs (such as
// AutoDL ComfyUI) therefore remain usable after the polling request finishes.
func MaterializeGenerationResult(ctx context.Context, channel *VideoChannel, result *GenerationResult) (*GenerationResult, error) {
	if !shouldMaterializeGenerationResult(channel) || result == nil || result.VideoURL == "" {
		return result, nil
	}
	trustedHosts := resultStorageTrustedHosts(channel)
	storedURL, err := transferGenerationResultURL(ctx, result.VideoURL, "video")
	if len(trustedHosts) > 0 {
		storedURL, err = transferGenerationResultURLTrusted(ctx, result.VideoURL, "video", trustedHosts)
	}
	if err != nil {
		return nil, fmt.Errorf("materialize video result: %w", err)
	}
	copy := *result
	copy.VideoURL = storedURL
	return &copy, nil
}
