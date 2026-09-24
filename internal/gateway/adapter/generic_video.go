package adapter

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/generic"
)

const GenericVideoAdapter = "generic@1"

func ValidateGenericVideoCatalog(baseURL, method, path, vendorModel string, config []byte) error {
	return generic.ValidateRuntimeCatalog(baseURL, method, path, vendorModel, config)
}

// ValidateCatalogProduct applies the same adapter-specific checks to edits as
// product creation. Generic mappings are executable configuration, so merely
// accepting syntactically valid JSON would defer a broken mapping to runtime.
func ValidateCatalogProduct(code string, version uint32, baseURL, method, path, vendorModel, taskScope string, config []byte) error {
	descriptor, ok := DescriptorFor(strings.ToLower(strings.TrimSpace(code)), version)
	if !ok {
		return repository.ErrInvalidInput
	}
	switch descriptor.Code {
	case "generic":
		return generic.ValidatePublishedRuntimeCatalog(baseURL, method, path, vendorModel, config)
	case "openai_images":
		return ValidateOpenAIImagesCatalog(method, path, vendorModel, taskScope, config)
	case "seedance":
		return ValidateSeedanceVideoCatalog(method, path)
	default:
		return nil
	}
}

// GenericVideo executes the signed json_task_v1 declaration stored with a
// catalog product. It is a codec only; AsyncDispatcher remains the sole HTTP
// sender and credential injector.
type GenericVideo struct{}

func (GenericVideo) Prepare(ctx context.Context, action string, fixed repository.AsyncDispatch, payload []byte, taskID string) (runtime.AsyncRequest, error) {
	codec, err := generic.NewRuntimeCodec(fixed.BaseURL, fixed.AdapterConfig)
	if err != nil {
		return runtime.AsyncRequest{}, err
	}
	var prepared generic.RuntimeRequest
	switch action {
	case "submit":
		if taskID != "" || fixed.VendorModel == "" {
			return runtime.AsyncRequest{}, repository.ErrInvalidInput
		}
		var input VideoRequest
		if err := json.Unmarshal(payload, &input); err != nil {
			return runtime.AsyncRequest{}, err
		}
		request := &video.GenerateRequest{
			Model: fixed.VendorModel, Prompt: input.Prompt, Resolution: input.Resolution,
			Ratio: input.Ratio, Duration: input.Duration, Audio: input.GenerateAudio,
			TaskMode: input.TaskMode, ServiceTier: input.ServiceTier, Content: input.Content,
			Params: input.Params, TaskID: fixed.PublicID,
		}
		prepared, err = codec.PrepareSubmit(ctx, request)
		if err == nil && (prepared.Method != fixed.Method || prepared.Path != fixed.Path) {
			return runtime.AsyncRequest{}, repository.ErrConflict
		}
	case "query":
		prepared, err = codec.PrepareQuery(taskID)
	default:
		return runtime.AsyncRequest{}, repository.ErrInvalidInput
	}
	if err != nil {
		return runtime.AsyncRequest{}, err
	}
	return runtime.AsyncRequest{
		Method: prepared.Method, Path: prepared.Path, Body: prepared.Body, Header: prepared.Header,
		CredentialHeader: prepared.CredentialHeader, CredentialPrefix: prepared.CredentialPrefix,
	}, nil
}

func (GenericVideo) Decode(action string, request, response []byte) (runtime.AsyncObservation, error) {
	// A generic response cannot be decoded without the catalog-pinned mapping.
	// AsyncDispatcher detects DecodeWithDispatch and never calls this fallback.
	return runtime.AsyncObservation{}, repository.ErrInvalidInput
}

// DecodeWithDispatch is used because generic response mappings are part of the
// immutable product configuration, while native adapters have a fixed schema.
func (GenericVideo) DecodeWithDispatch(action string, fixed repository.AsyncDispatch, request, response []byte) (runtime.AsyncObservation, error) {
	codec, err := generic.NewRuntimeCodec(fixed.BaseURL, fixed.AdapterConfig)
	if err != nil {
		return runtime.AsyncObservation{}, err
	}
	var decoded generic.RuntimeResponse
	switch action {
	case "submit":
		decoded, err = codec.DecodeSubmit(response)
	case "query":
		decoded, err = codec.DecodePoll(response)
	default:
		return runtime.AsyncObservation{}, repository.ErrInvalidInput
	}
	if err != nil {
		return runtime.AsyncObservation{}, err
	}
	out := runtime.AsyncObservation{
		TaskID: decoded.ProviderTaskID,
		Facts: billing.Facts{
			Events:     map[billing.ChargeEvent]bool{billing.ChargeAccepted: true},
			Quantities: make(map[billing.QuantitySource]string),
		},
	}
	switch decoded.Status {
	case video.VideoTaskStatusQueued, video.VideoTaskStatusSubmitted:
		out.State = execution.AsyncAccepted
	case video.VideoTaskStatusTracking:
		out.State = execution.AsyncRunning
	case video.VideoTaskStatusCompleted:
		out.State = execution.AsyncSucceeded
		if decoded.Result == nil {
			return runtime.AsyncObservation{}, delivery.ErrInvalidResult
		}
		out.Sources = []delivery.RemoteResult{{Role: "video", URL: decoded.Result.VideoURL}}
		if decoded.Result.ThumbnailURL != "" {
			out.Sources = append(out.Sources, delivery.RemoteResult{Role: "thumbnail", URL: decoded.Result.ThumbnailURL})
		}
		if err := delivery.ValidateVideoSources(out.Sources); err != nil {
			return runtime.AsyncObservation{}, err
		}
		out.Facts.Quantities[billing.QuantityGeneratedVideos] = "1"
	case video.VideoTaskStatusFailed:
		out.State = execution.AsyncFailed
	case video.VideoTaskStatusCancelled:
		out.State = execution.AsyncCancelled
	default:
		return runtime.AsyncObservation{}, repository.ErrInvalidInput
	}
	var input VideoRequest
	if err := json.Unmarshal(request, &input); err != nil {
		return runtime.AsyncObservation{}, err
	}
	var generatedSeconds string
	if input.Duration > 0 {
		out.Facts.Quantities[billing.QuantityRequestedSeconds] = strconv.Itoa(input.Duration)
	}
	if out.State == execution.AsyncSucceeded {
		result := delivery.VideoResult{SchemaVersion: delivery.ResultSchemaVersion, Duration: strings.TrimSpace(decoded.Duration)}
		if result.Duration != "" {
			amount, err := billing.ParseAmount(result.Duration, 18, true)
			if err != nil {
				return runtime.AsyncObservation{}, err
			}
			result.Duration = amount.String()
			generatedSeconds = result.Duration
			out.Facts.Quantities[billing.QuantityGeneratedSeconds] = result.Duration
		}
		out.Result, err = json.Marshal(result)
	}
	env, envErr := videoBillingFacts(input, generatedSeconds, out.State == execution.AsyncSucceeded)
	if envErr != nil {
		return runtime.AsyncObservation{}, envErr
	}
	out.Facts.Expr = env
	return out, err
}

// Compile-time assertions are kept here because GenericVideo uses the extended
// dispatch-aware decoder rather than the fixed-schema Decode method.
var _ runtime.AsyncCodec = GenericVideo{}
