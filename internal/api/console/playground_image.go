package console

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/open"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

const (
	playgroundImageGenerationPath = "/v1/images/generations"
	playgroundImageEditPath       = "/v1/images/edits"
)

type playgroundImageModel struct {
	ID                 string                       `json:"id"`
	Name               string                       `json:"name"`
	Description        string                       `json:"description,omitempty"`
	SupportsGeneration bool                         `json:"supports_generation"`
	SupportsEdit       bool                         `json:"supports_edit"`
	GenerationOptions  *playgroundImageModelOptions `json:"generation_options,omitempty"`
	EditOptions        *playgroundImageModelOptions `json:"edit_options,omitempty"`
}

type playgroundImageModelOptions struct {
	Sizes        []string `json:"sizes,omitempty"`
	AspectRatios []string `json:"aspect_ratios,omitempty"`
	Qualities    []string `json:"qualities,omitempty"`
}

type playgroundImageModelsResponse struct {
	Models []playgroundImageModel `json:"models"`
}

// PlaygroundListImageModels GET /api/playground/:token_id/images/models.
func PlaygroundListImageModels(c *gin.Context) {
	if _, ok := getPlaygroundToken(c); !ok {
		return
	}

	result, err := listPlaygroundImageModels(c.Request.Context())
	if err != nil {
		resp.ErrorMsg(c, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "image catalog is unavailable")
		return
	}
	resp.Success(c, result)
}

func listPlaygroundImageModels(ctx context.Context) (*playgroundImageModelsResponse, error) {
	db, err := model.DB().DB()
	if err != nil {
		return nil, err
	}
	store, err := repository.New(db)
	if err != nil {
		return nil, err
	}
	rows, err := store.ListActivePublicCatalog(ctx)
	if err != nil {
		return nil, err
	}

	models := make([]playgroundImageModel, 0)
	indexes := make(map[string]int)
	for _, row := range rows {
		if strings.ToUpper(strings.TrimSpace(row.DownstreamHTTPMethod)) != http.MethodPost {
			continue
		}
		path := strings.TrimSpace(row.DownstreamPath)
		if path != playgroundImageGenerationPath && path != playgroundImageEditPath {
			continue
		}
		var serviceTiers []string
		if err := json.Unmarshal(row.ServiceTiers, &serviceTiers); err != nil {
			return nil, err
		}
		supportsStandard := false
		for _, tier := range serviceTiers {
			if strings.EqualFold(strings.TrimSpace(tier), "standard") {
				supportsStandard = true
				break
			}
		}
		if !supportsStandard {
			continue
		}

		id := strings.TrimSpace(row.APIName)
		if id == "" {
			continue
		}
		index, exists := indexes[id]
		if !exists {
			name := strings.TrimSpace(row.DisplayName)
			if name == "" {
				name = id
			}
			index = len(models)
			indexes[id] = index
			models = append(models, playgroundImageModel{
				ID:          id,
				Name:        name,
				Description: strings.TrimSpace(row.Description),
			})
		}
		var options playgroundImageModelOptions
		if len(row.CapabilityConstraints) > 0 {
			if err := json.Unmarshal(row.CapabilityConstraints, &options); err != nil {
				return nil, err
			}
		}
		if path == playgroundImageGenerationPath {
			models[index].SupportsGeneration = true
			models[index].GenerationOptions = mergePlaygroundImageOptions(models[index].GenerationOptions, options)
		} else {
			models[index].SupportsEdit = true
			models[index].EditOptions = mergePlaygroundImageOptions(models[index].EditOptions, options)
		}
	}

	return &playgroundImageModelsResponse{Models: models}, nil
}

func mergePlaygroundImageOptions(current *playgroundImageModelOptions, next playgroundImageModelOptions) *playgroundImageModelOptions {
	if current == nil {
		current = &playgroundImageModelOptions{}
	}
	current.Sizes = appendUniqueImageOptions(current.Sizes, next.Sizes...)
	current.AspectRatios = appendUniqueImageOptions(current.AspectRatios, next.AspectRatios...)
	current.Qualities = appendUniqueImageOptions(current.Qualities, next.Qualities...)
	if len(current.Sizes) == 0 && len(current.AspectRatios) == 0 && len(current.Qualities) == 0 {
		return nil
	}
	return current
}

func appendUniqueImageOptions(current []string, values ...string) []string {
	seen := make(map[string]struct{}, len(current)+len(values))
	result := make([]string, 0, len(current)+len(values))
	for _, value := range append(append([]string(nil), current...), values...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// PlaygroundCreateImageGeneration POST /api/playground/:token_id/images/generations.
func PlaygroundCreateImageGeneration(c *gin.Context) {
	if _, ok := usePlaygroundToken(c); !ok {
		return
	}
	open.CreateImageGenerationOpenAI(c)
}

// PlaygroundCreateImageEdit POST /api/playground/:token_id/images/edits.
func PlaygroundCreateImageEdit(c *gin.Context) {
	if _, ok := usePlaygroundToken(c); !ok {
		return
	}
	open.CreateImageEditOpenAI(c)
}
