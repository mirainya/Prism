//go:build integration

package migrate

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/google/uuid"
	gatewaybilling "github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	"github.com/mirainya/Prism/internal/model"
)

type engineIsolationSelector struct{ route routing.RouteResult }

func (s *engineIsolationSelector) SelectTransport(context.Context, string, routing.RouteRequirements, routing.RouteOptions) (*routing.RouteResult, error) {
	selected := s.route
	return &selected, nil
}

func (*engineIsolationSelector) Release(uint) {}

type engineIsolationTransport struct {
	fail   bool
	stream bool
}

func (*engineIsolationTransport) ID() transport.ID { return transport.OpenAIChat }

func (*engineIsolationTransport) Plan(operation transport.Operation, _ canonical.Request, features canonical.FeatureSet) transport.Plan {
	return transport.Exact(operation, features)
}

func (t *engineIsolationTransport) Prepare(_ context.Context, invocation transport.Invocation) (transport.PreparedRequest, error) {
	return transport.PreparedRequest{
		Method:  http.MethodPost,
		URL:     "https://unified-engine.test/execute",
		Headers: http.Header{"Content-Type": []string{"application/json"}},
		Body:    []byte(`{"model":"unified-proof"}`),
		Stream:  invocation.Request.Stream,
	}, nil
}

func (t *engineIsolationTransport) ExecutePrepared(context.Context, transport.Invocation, transport.PreparedRequest) (canonical.Response, error) {
	if t.fail {
		return canonical.Response{}, errors.New("isolated upstream failure")
	}
	return engineIsolationResponse(), nil
}

func (t *engineIsolationTransport) StreamPrepared(context.Context, transport.Invocation, transport.PreparedRequest) (transport.EventStream, error) {
	if !t.stream {
		return nil, errors.New("unexpected stream execution")
	}
	response := engineIsolationResponse()
	return &engineIsolationStream{event: canonical.Event{
		Type: canonical.EventCompleted, Response: &response, Usage: response.Usage,
		ProviderResponseID: response.ProviderResponseID,
	}}, nil
}

func engineIsolationResponse() canonical.Response {
	return canonical.Response{
		ID: "provider-unified-proof", ProviderResponseID: "provider-unified-proof", Status: "completed",
		Output: []canonical.Item{{
			Type: "message", Role: canonical.RoleAssistant,
			Content: []canonical.Content{{Type: "output_text", Text: "complete"}},
		}},
		Usage: &canonical.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3},
	}
}

type engineIsolationStream struct {
	event canonical.Event
	done  bool
}

func (s *engineIsolationStream) Next(context.Context) (canonical.Event, error) {
	if s.done {
		return canonical.Event{}, io.EOF
	}
	s.done = true
	return s.event, nil
}

func (*engineIsolationStream) Close() error { return nil }

type legacyExecutionCounts struct {
	calls, attempts, payloads, billing, requests int64
}

func verifyUnifiedEngineIsolation(t *testing.T, db *sql.DB, call repository.CreateCallInput, attempt repository.BeginAttemptInput) {
	t.Helper()
	var channelID uint
	if err := db.QueryRow(`SELECT channel_id FROM gw_credential_pools WHERE id=?`, attempt.CredentialPoolID).Scan(&channelID); err != nil {
		t.Fatal(err)
	}

	schedule := gatewaybilling.RateSchedule{
		Currency: gatewaybilling.Currency{
			Code: call.Currency, Version: call.CurrencyVersion, FractionDigits: 8,
			RoundingMode: "half_even", MaxAmount: "1000000",
		},
		Components: []gatewaybilling.RateComponent{{
			Code: "request", Unit: "request", Source: gatewaybilling.QuantityOne,
			Event: gatewaybilling.ChargeSucceeded, UnitPrice: "0.01", UnitScale: 0,
			QuantityStep: "0", MaxQuantity: "1",
		}},
	}
	baseRoute := routing.RouteResult{
		ReleaseID: uint(attempt.CatalogReleaseID), OperationContractID: uint(call.OperationContractID),
		ModelOperationID: uint(call.ModelOperationID), SKUID: uint(attempt.SKUID), RouteID: uint(attempt.RouteID),
		OfferingID: uint(attempt.OfferingID), CostPlanID: uint(attempt.CostPlanID), ProductTransportID: uint(attempt.ProductTransportID),
		CredentialPoolID: uint(attempt.CredentialPoolID), CredentialID: uint(attempt.CredentialID),
		CredentialVersionID: uint(attempt.CredentialVersionID), PurposeGrantID: uint(attempt.PurposeGrantID),
		AbilityID: uint(call.ModelOperationID), KeyID: uint(attempt.CredentialID), ChannelID: channelID,
		Protocol: model.ProtocolOpenAI, BaseURL: "https://unified-engine.test", APIKey: "not-persisted",
		VendorModel: "unified-proof", ModelName: "unified-proof", Transport: transport.OpenAIChat,
		Currency: call.Currency, CurrencyVersion: uint(call.CurrencyVersion), DeliveryMode: call.DeliveryMode,
		SellSchedule: &schedule,
	}

	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", "KSkpKSkpKSkpKSkpKSkpKSkpKSkpKSkpKSkpKSkpKSk=")
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", "KioqKioqKioqKioqKioqKioqKioqKioqKioqKioqKio=")

	testCases := []struct {
		name     string
		endpoint canonical.Endpoint
		stream   bool
		fail     bool
		status   string
	}{
		{name: "chat_success", endpoint: canonical.EndpointOpenAIChat, status: "completed"},
		{name: "chat_failure", endpoint: canonical.EndpointOpenAIChat, fail: true, status: "failed"},
		{name: "chat_stream", endpoint: canonical.EndpointOpenAIChat, stream: true, status: "completed"},
		{name: "messages_success", endpoint: canonical.EndpointAnthropic, status: "completed"},
		{name: "responses_success", endpoint: canonical.EndpointOpenAIResponses, status: "completed"},
	}
	for _, testCase := range testCases {
		t.Run("unified_engine_isolation_"+testCase.name, func(t *testing.T) {
			before := readLegacyExecutionCounts(t, db)
			registry := transport.NewRegistry()
			upstream := &engineIsolationTransport{fail: testCase.fail, stream: testCase.stream}
			if err := registry.Register(upstream); err != nil {
				t.Fatal(err)
			}
			registry.Freeze()
			executionEngine, err := engine.New(&engineIsolationSelector{route: baseRoute}, registry)
			if err != nil {
				t.Fatal(err)
			}
			publicID := uuid.NewString()
			result, executeErr := executionEngine.Execute(context.Background(), canonical.Request{
				Endpoint: testCase.endpoint, Model: "unified-proof", Stream: testCase.stream,
			}, engine.ExecuteOptions{
				UserID: uint(call.UserID), TokenID: uint(call.TokenID), CallID: publicID,
				RequestID: "unified-engine-isolation-" + testCase.name,
			})
			if testCase.fail {
				if executeErr == nil || result != nil {
					t.Fatalf("failure result=%#v err=%v", result, executeErr)
				}
			} else {
				if executeErr != nil || result == nil {
					t.Fatalf("result=%#v err=%v", result, executeErr)
				}
				if testCase.stream {
					event, err := result.Stream.Next(context.Background())
					if err != nil || event.Type != canonical.EventCompleted {
						t.Fatalf("terminal event=%#v err=%v", event, err)
					}
					if err := result.Stream.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			after := readLegacyExecutionCounts(t, db)
			if after != before {
				t.Fatalf("unified execution changed legacy tables: before=%+v after=%+v", before, after)
			}
			assertUnifiedEngineFacts(t, db, publicID, testCase.status, !testCase.fail)
		})
	}
}

func readLegacyExecutionCounts(t *testing.T, db *sql.DB) legacyExecutionCounts {
	t.Helper()
	var result legacyExecutionCounts
	err := db.QueryRow(`SELECT
  (SELECT COUNT(*) FROM api_calls),
  (SELECT COUNT(*) FROM api_call_attempts),
  (SELECT COUNT(*) FROM api_call_payloads),
  (SELECT COUNT(*) FROM billing_logs),
  (SELECT COUNT(*) FROM channel_request_logs)`).Scan(
		&result.calls, &result.attempts, &result.payloads, &result.billing, &result.requests,
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertUnifiedEngineFacts(t *testing.T, db *sql.DB, publicID, status string, succeeded bool) {
	t.Helper()
	var callID uint64
	var storedStatus string
	if err := db.QueryRow(`SELECT id,status FROM gw_api_calls WHERE public_id=?`, publicID).Scan(&callID, &storedStatus); err != nil {
		t.Fatal(err)
	}
	if storedStatus != status {
		t.Fatalf("call status=%s want=%s", storedStatus, status)
	}
	var attempts, requests, reservations, requestPayloads, resultPayloads int
	if err := db.QueryRow(`SELECT
  (SELECT COUNT(*) FROM gw_api_call_attempts WHERE call_id=?),
  (SELECT COUNT(*) FROM gw_channel_request_logs WHERE attempt_id IN (SELECT id FROM gw_api_call_attempts WHERE call_id=?)),
  (SELECT COUNT(*) FROM billing_reservations WHERE call_id=?),
  (SELECT COUNT(*) FROM gw_api_call_payloads WHERE call_id=? AND kind='request'),
  (SELECT COUNT(*) FROM gw_api_call_payloads WHERE call_id=? AND kind='result')`,
		callID, callID, callID, callID, callID,
	).Scan(&attempts, &requests, &reservations, &requestPayloads, &resultPayloads); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || requests != 1 || reservations != 1 || requestPayloads != 1 {
		t.Fatalf("incomplete unified facts: attempts=%d requests=%d reservations=%d request_payloads=%d", attempts, requests, reservations, requestPayloads)
	}
	wantResultPayloads := 0
	if succeeded {
		wantResultPayloads = 1
	}
	if resultPayloads != wantResultPayloads {
		t.Fatalf("result payloads=%d want=%d", resultPayloads, wantResultPayloads)
	}
}
