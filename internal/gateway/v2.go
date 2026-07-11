package gateway

import (
	"net/http"

	"github.com/mirainya/Prism/internal/gateway/engine"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/gateway/transport"
	transportanthropic "github.com/mirainya/Prism/internal/gateway/transport/anthropic"
	transportgoogle "github.com/mirainya/Prism/internal/gateway/transport/google"
	transportopenai "github.com/mirainya/Prism/internal/gateway/transport/openai"
	transportvolcengine "github.com/mirainya/Prism/internal/gateway/transport/volcengine"
	"github.com/mirainya/Prism/internal/service"
)

// NewV2Engine is the sole composition point for Gateway V2 transports.
func NewV2Engine() (*engine.Engine, error) {
	// Streaming requests can legitimately stay open for much longer than a
	// completion timeout. Dial/TLS/header timeouts belong on the transport.
	client := &http.Client{}
	registry := transport.NewRegistry()
	for _, item := range []transport.Transport{
		transportopenai.NewChat(client),
		transportopenai.NewResponses(client),
		transportanthropic.New(client),
		transportgoogle.NewGenerateContent(client),
		transportvolcengine.New(transport.HTTPClient{Client: client}),
	} {
		if err := registry.Register(item); err != nil {
			return nil, err
		}
	}
	registry.Freeze()
	return engine.New(routing.NewRouter(), registry, service.NewBillingService())
}
