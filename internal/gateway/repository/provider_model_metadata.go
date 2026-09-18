package repository

import (
	"context"
	"strings"
	"time"
)

// ProviderModelMetadataKey identifies one upstream model inside a channel. The
// same model name may exist on unrelated providers, so model code alone is not
// a safe lookup key.
type ProviderModelMetadataKey struct {
	ChannelID uint64
	ModelCode string
}

type ProviderModelMetadata struct {
	Description string
	Tags        string
	ObservedAt  time.Time
}

func NewProviderModelMetadataKey(channelID uint64, modelCode string) ProviderModelMetadataKey {
	return ProviderModelMetadataKey{ChannelID: channelID, ModelCode: strings.ToLower(strings.TrimSpace(modelCode))}
}

// LoadProviderModelMetadata returns the latest validated discovery snapshot
// for every active AiCost source. This metadata is advisory and may change
// independently from an immutable release; manually maintained catalog text
// remains authoritative at the service layer.
func (s *Store) LoadProviderModelMetadata(ctx context.Context) (map[ProviderModelMetadataKey]ProviderModelMetadata, error) {
	if s == nil || s.db == nil || ctx == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT src.channel_id,item.model_code,item.description,item.tags,snapshot.observed_at
FROM gw_catalog_sources src
JOIN gw_catalog_source_profiles profile ON profile.catalog_source_id=src.id AND profile.contract_code=?
JOIN gw_catalog_release_sources release_source ON release_source.catalog_source_id=src.id
JOIN gw_catalog_discovery_snapshots snapshot ON snapshot.release_source_id=release_source.id
JOIN (
	SELECT release_source.catalog_source_id,MAX(snapshot.id) AS snapshot_id
	FROM gw_catalog_release_sources release_source
	JOIN gw_catalog_discovery_snapshots snapshot ON snapshot.release_source_id=release_source.id
	GROUP BY release_source.catalog_source_id
) latest ON latest.catalog_source_id=src.id AND latest.snapshot_id=snapshot.id
JOIN gw_catalog_discovery_items item ON item.snapshot_id=snapshot.id
WHERE src.status='active'
ORDER BY snapshot.observed_at DESC,snapshot.id DESC,item.ordinal`, "aicost_pricing_v1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[ProviderModelMetadataKey]ProviderModelMetadata)
	for rows.Next() {
		var channelID uint64
		var modelCode, description, tags string
		var observed time.Time
		if err := rows.Scan(&channelID, &modelCode, &description, &tags, &observed); err != nil {
			return nil, err
		}
		key := NewProviderModelMetadataKey(channelID, modelCode)
		if key.ChannelID == 0 || key.ModelCode == "" {
			continue
		}
		if _, exists := result[key]; exists {
			continue
		}
		result[key] = ProviderModelMetadata{
			Description: strings.TrimSpace(description), Tags: strings.TrimSpace(tags), ObservedAt: observed.UTC(),
		}
	}
	return result, rows.Err()
}
