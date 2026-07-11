// Package limits contains request limits shared by the protocol entrypoints.
package limits

import (
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/model"
)

// ApplyModelMaxOutputTokens caps an explicitly requested output budget using
// the model metadata value. A missing or zero metadata value leaves the
// request unchanged; provider transports may still enforce their own limit.
func ApplyModelMaxOutputTokens(request canonical.Request, modelName string) canonical.Request {
	if request.MaxOutputTokens == nil || *request.MaxOutputTokens <= 0 || modelName == "" {
		return request
	}
	var meta model.GwModelMeta
	if err := model.DB().Select("max_tokens").Where("model_name = ?", modelName).First(&meta).Error; err != nil || meta.MaxTokens <= 0 || *request.MaxOutputTokens <= meta.MaxTokens {
		return request
	}
	value := meta.MaxTokens
	request.MaxOutputTokens = &value
	return request
}
