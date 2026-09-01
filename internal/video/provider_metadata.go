package video

import (
	"context"
	"encoding/json"

	"github.com/mirainya/Prism/internal/model"
	"gorm.io/datatypes"
)

func SaveProviderMetadata(ctx context.Context, taskID string, metadata *ProviderMetadata) error {
	if metadata == nil || taskID == "" {
		return nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	return model.DB().WithContext(ctx).Model(&VideoTask{}).Where("id = ?", taskID).
		Update("provider_metadata", datatypes.JSON(encoded)).Error
}

func DecodeProviderMetadata(raw datatypes.JSON) *ProviderMetadata {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var metadata ProviderMetadata
	if json.Unmarshal(raw, &metadata) != nil {
		return nil
	}
	return &metadata
}
