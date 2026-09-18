package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/delivery"
	"github.com/mirainya/Prism/internal/gateway/execution"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/video"
	"github.com/mirainya/Prism/internal/video/seedance"
)

const SeedanceAdapter = "seedance@1"
const SeedanceSubmitPath = "/api/v3/contents/generations/tasks"

func ValidateSeedanceVideoCatalog(method, path string) error {
	if strings.ToUpper(strings.TrimSpace(method)) != http.MethodPost || strings.TrimSpace(path) != SeedanceSubmitPath {
		return repository.ErrInvalidInput
	}
	return nil
}

// VideoRequest is the executable, provider-neutral payload. Routing replaces
// the public model with the pinned vendor model only when encoding upstream.
type VideoRequest struct {
	Model         string              `json:"model"`
	Prompt        string              `json:"prompt"`
	TaskMode      string              `json:"task_mode,omitempty"`
	Resolution    string              `json:"resolution"`
	Ratio         string              `json:"ratio"`
	Duration      int                 `json:"duration"`
	GenerateAudio bool                `json:"generate_audio"`
	ServiceTier   string              `json:"service_tier,omitempty"`
	Content       []video.ContentItem `json:"content,omitempty"`
	Params        map[string]any      `json:"params,omitempty"`
}

type Seedance struct{}

func (Seedance) Prepare(ctx context.Context, action string, fixed repository.AsyncDispatch, payload []byte, taskID string) (runtime.AsyncRequest, error) {
	if action == "query" {
		if taskID == "" || taskID == "." || taskID == ".." {
			return runtime.AsyncRequest{}, repository.ErrInvalidInput
		}
		return runtime.AsyncRequest{Method: http.MethodGet, Path: strings.TrimRight(fixed.Path, "/") + "/" + url.PathEscape(taskID)}, nil
	}
	if action != "submit" || fixed.Method != http.MethodPost || fixed.VendorModel == "" {
		return runtime.AsyncRequest{}, repository.ErrInvalidInput
	}
	var input VideoRequest
	if err := json.Unmarshal(payload, &input); err != nil {
		return runtime.AsyncRequest{}, err
	}
	request := &video.GenerateRequest{Model: fixed.VendorModel, Prompt: input.Prompt, Resolution: input.Resolution,
		Ratio: input.Ratio, Duration: input.Duration, Audio: input.GenerateAudio, TaskMode: input.TaskMode, ServiceTier: input.ServiceTier, Content: input.Content, Params: input.Params, TaskID: fixed.PublicID}
	codec := seedance.Codec{}
	if err := codec.ValidateRequest(ctx, request); err != nil {
		return runtime.AsyncRequest{}, err
	}
	prepared, err := codec.BuildRequest(ctx, request)
	if err != nil {
		return runtime.AsyncRequest{}, err
	}
	body, err := json.Marshal(prepared.Body)
	if err != nil {
		return runtime.AsyncRequest{}, err
	}
	header := make(http.Header, len(prepared.Headers))
	for name, value := range prepared.Headers {
		header.Set(name, value)
	}
	return runtime.AsyncRequest{Method: fixed.Method, Path: fixed.Path, Body: body, Header: header}, nil
}

func (Seedance) Decode(action string, request, response []byte) (runtime.AsyncObservation, error) {
	codec := seedance.Codec{}
	if action == "submit" {
		accepted, err := codec.DecodeSubmit(response)
		if err != nil {
			return runtime.AsyncObservation{}, err
		}
		var input VideoRequest
		if len(request) != 0 {
			if err := json.Unmarshal(request, &input); err != nil {
				return runtime.AsyncObservation{}, err
			}
		}
		env, err := videoBillingFacts(input, "", false)
		if err != nil {
			return runtime.AsyncObservation{}, err
		}
		return runtime.AsyncObservation{TaskID: accepted.ProviderTaskID, State: execution.AsyncAccepted,
			Facts: billing.Facts{Events: map[billing.ChargeEvent]bool{billing.ChargeAccepted: true}, Expr: env}}, nil
	}
	if action != "query" {
		return runtime.AsyncObservation{}, repository.ErrInvalidInput
	}
	progress, err := codec.DecodePoll(response)
	if err != nil {
		return runtime.AsyncObservation{}, err
	}
	out := runtime.AsyncObservation{Facts: billing.Facts{Events: map[billing.ChargeEvent]bool{billing.ChargeAccepted: true}, Quantities: make(map[billing.QuantitySource]string)}}
	out.TaskID, err = codec.DecodeTaskIdentity(response)
	if err != nil {
		return runtime.AsyncObservation{}, err
	}
	switch progress.Status {
	case video.VideoTaskStatusSubmitted:
		out.State = execution.AsyncAccepted
	case video.VideoTaskStatusTracking:
		out.State = execution.AsyncRunning
	case video.VideoTaskStatusCompleted:
		out.State = execution.AsyncSucceeded
		if progress.Result == nil {
			return runtime.AsyncObservation{}, delivery.ErrInvalidResult
		}
		out.Sources = []delivery.RemoteResult{{Role: "video", URL: progress.Result.VideoURL}}
		if progress.Result.ThumbnailURL != "" {
			out.Sources = append(out.Sources, delivery.RemoteResult{Role: "thumbnail", URL: progress.Result.ThumbnailURL})
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
	if err != nil {
		return runtime.AsyncObservation{}, err
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
		result := delivery.VideoResult{SchemaVersion: delivery.ResultSchemaVersion}
		// Billing parses the original JSON number, never the compatibility
		// projection's float64 duration.
		var data struct {
			Duration json.Number `json:"duration"`
			Result   *struct {
				Duration json.Number `json:"duration"`
			} `json:"result"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response, &data); err != nil {
			return runtime.AsyncObservation{}, err
		}
		if len(data.Data) > 0 {
			if err := json.Unmarshal(data.Data, &data); err != nil {
				return runtime.AsyncObservation{}, err
			}
		}
		duration := data.Duration.String()
		if data.Result != nil {
			duration = data.Result.Duration.String()
		}
		if duration != "" {
			value, err := billing.ParseAmount(duration, 18, true)
			if err != nil {
				return runtime.AsyncObservation{}, err
			}
			out.Facts.Quantities[billing.QuantityGeneratedSeconds] = value.String()
			generatedSeconds = value.String()
			result.Duration = value.String()
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
