package repository

import (
	"errors"
	"testing"
)

func TestTransportTimeoutChangeValidation(t *testing.T) {
	base := TransportTimeoutChange{
		catalogChangeGuard: catalogChangeGuard{ExpectedActiveReleaseID: 2, ExpectedConfigVersion: 7},
		TransportCode:      " AICOST-VIDEO ",
		TimeoutMS:          120000,
	}
	base.Normalize()
	if err := base.Validate(); err != nil {
		t.Fatalf("valid timeout change rejected: %v", err)
	}
	if base.TransportCode != "aicost-video" {
		t.Fatalf("transport code was not normalized: %q", base.TransportCode)
	}
	for name, mutate := range map[string]func(*TransportTimeoutChange){
		"missing release":   func(value *TransportTimeoutChange) { value.ExpectedActiveReleaseID = 0 },
		"missing version":   func(value *TransportTimeoutChange) { value.ExpectedConfigVersion = 0 },
		"missing transport": func(value *TransportTimeoutChange) { value.TransportCode = "" },
		"too short":         func(value *TransportTimeoutChange) { value.TimeoutMS = 99 },
		"too long":          func(value *TransportTimeoutChange) { value.TimeoutMS = 900001 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Validate() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
