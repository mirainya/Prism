package responses

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	openairesponses "github.com/mirainya/Prism/internal/gateway/codec/openai_responses"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	protocol "github.com/mirainya/Prism/internal/provider/responses"
)

type V2Executor struct {
	engine *engine.Engine
}

type V2Result struct {
	Response           *protocol.Response
	CanonicalResponse  *canonical.Response
	Route              *routing.RouteResult
	Prepared           transport.PreparedRequest
	ProviderResponseID string
	RequestLogID       uint
	AttemptID          uint
	execution          *engine.Result
}

func NewV2Executor(executionEngine *engine.Engine) (*V2Executor, error) {
	if executionEngine == nil {
		return nil, errors.New("Gateway V2 engine is required")
	}
	return &V2Executor{engine: executionEngine}, nil
}

// Execute runs one non-streaming Responses request through Gateway V2.
// publicResponseID is the Prism-visible identifier allocated by persistence;
// a new identifier is generated when the caller has not allocated one yet.
func (e *V2Executor) Execute(ctx context.Context, request *protocol.Request, publicResponseID string, options engine.ExecuteOptions) (*V2Result, error) {
	if e == nil || e.engine == nil {
		return nil, errors.New("Gateway V2 executor is not initialized")
	}
	if request == nil {
		return nil, errors.New("Responses request is required")
	}
	if request.Stream {
		return nil, errors.New("V2Executor only supports non-streaming Responses requests")
	}
	canonicalRequest, err := openairesponses.DecodeRequest(*request)
	if err != nil {
		return nil, err
	}
	canonicalRequest.Stream = false
	result, err := e.engine.Execute(ctx, canonicalRequest, options)
	if err != nil {
		return nil, err
	}
	if result == nil || result.Response == nil || result.Stream != nil {
		return nil, errors.New("Gateway V2 returned no non-streaming response")
	}

	providerResponseID := result.Response.ProviderResponseID
	if providerResponseID == "" {
		providerResponseID = result.Response.ID
	}
	if strings.TrimSpace(publicResponseID) == "" {
		publicResponseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	canonicalResponse := *result.Response
	canonicalResponse.Output = canonical.CloneItems(result.Response.Output)
	result.Response.ID = publicResponseID
	result.Response.Model = request.Model
	if result.Response.CreatedAt == 0 {
		result.Response.CreatedAt = time.Now().Unix()
	}
	encoded, err := openairesponses.EncodeResponseJSON(*result.Response)
	if err != nil {
		_ = result.FailDelivery(err, false)
		return nil, err
	}
	var response protocol.Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		_ = result.FailDelivery(err, false)
		return nil, err
	}
	response.ID = publicResponseID
	response.Model = request.Model
	response.Object = "response"
	response.PreviousResponseID = nil
	if request.PreviousResponseID != "" {
		previous := request.PreviousResponseID
		response.PreviousResponseID = &previous
	}
	if request.Store == nil {
		response.Store = true
	} else {
		response.Store = *request.Store
	}
	response.Background = request.Background

	return &V2Result{
		Response:           &response,
		CanonicalResponse:  &canonicalResponse,
		Route:              result.Route,
		Prepared:           result.Prepared,
		ProviderResponseID: providerResponseID,
		RequestLogID:       result.RequestLogID,
		AttemptID:          result.AttemptID,
		execution:          result,
	}, nil
}
