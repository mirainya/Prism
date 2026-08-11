package video

import (
	"errors"
	"fmt"
)

type ProviderErrorKind string

const (
	ProviderErrorRetryable ProviderErrorKind = "retryable"
	ProviderErrorAmbiguous ProviderErrorKind = "ambiguous"
)

type ProviderError struct {
	Kind ProviderErrorKind
	Op   string
	Err  error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "video provider error"
	}
	if e.Err == nil {
		if e.Op == "" {
			return "video provider error"
		}
		return e.Op + ": video provider error"
	}
	if e.Op == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewRetryableProviderError(op string, err error) error {
	if err == nil {
		err = errors.New("retryable provider failure")
	}
	return &ProviderError{Kind: ProviderErrorRetryable, Op: op, Err: err}
}

func NewAmbiguousProviderError(op string, err error) error {
	if err == nil {
		err = errors.New("ambiguous provider result")
	}
	return &ProviderError{Kind: ProviderErrorAmbiguous, Op: op, Err: err}
}

func IsRetryableProviderError(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) &&
		(providerErr.Kind == ProviderErrorRetryable || providerErr.Kind == ProviderErrorAmbiguous)
}

func IsAmbiguousProviderError(err error) bool {
	var providerErr *ProviderError
	return errors.As(err, &providerErr) && providerErr.Kind == ProviderErrorAmbiguous
}
