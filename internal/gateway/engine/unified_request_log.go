package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/internal/gateway/repository"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/gateway/security"
	"github.com/mirainya/Prism/internal/gateway/transport"
)

type unifiedRequestLog struct {
	service *gatewayruntime.Service
	id      uint64
}

func (l *callLifecycle) unifiedRequestOwner() *unifiedLifecycle {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.unified
}

func beginUnifiedRequestLog(owner *unifiedLifecycle, prepared transport.PreparedRequest, operation transport.Operation) (*unifiedRequestLog, error) {
	if owner == nil {
		return nil, nil
	}
	key, err := decodeGatewayHMACKey("PRISM_GATEWAY_PAYLOAD_HMAC_B64")
	if err != nil {
		return nil, err
	}
	defer clear(key)
	mapping, err := json.Marshal(struct {
		Operation transport.Operation
		Method    string
		URL       string
		Headers   map[string][]string
		Body      []byte
	}{operation, prepared.Method, prepared.URL, prepared.Headers, prepared.Body})
	if err != nil {
		return nil, err
	}
	defer clear(mapping)
	digest := security.HMACSHA256(key, mapping)
	service, err := gatewayruntime.New(owner.store)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id, err := service.BeginRequest(ctx, repository.RequestLogInput{AttemptID: &owner.attemptID, RequestSeq: 1, Action: "submit", MappingHMAC: fmt.Sprintf("%x", digest[:])})
	if err != nil {
		return nil, unifiedAdmissionError(err)
	}
	return &unifiedRequestLog{service: service, id: id}, nil
}

func unifiedAdmissionError(err error) error {
	if errors.Is(err, repository.ErrConcurrencyLimit) {
		return &domain.AppError{HTTPStatus: http.StatusTooManyRequests, Code: "credential_capacity_exhausted", Message: "Upstream concurrency capacity is currently exhausted", Err: err}
	}
	return err
}

func (l *unifiedRequestLog) finish(hasResponse bool, status int, requestErr error, duration time.Duration) error {
	if l == nil {
		return nil
	}
	result := repository.RequestLogResult{}
	elapsed := uint64(max(duration.Milliseconds(), 0))
	result.DurationMS = &elapsed
	var providerStatus interface{ HTTPStatus() int }
	if status == 0 && errors.As(requestErr, &providerStatus) {
		status = providerStatus.HTTPStatus()
	}
	if status >= 100 && status <= 599 {
		value := uint16(status)
		result.HTTPStatus = &value
	}
	outcome := "unknown"
	if hasResponse || result.HTTPStatus != nil {
		outcome = "response_recorded"
	}
	if requestErr != nil {
		result.ErrorCode = "exchange_error"
	}
	// Canonical responses are not raw HTTP bytes; byte-completeness stays unset.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return l.service.FinishRequest(ctx, l.id, outcome, result)
}
