package admin

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
)

func TestChannelAccountResponseMasksAPIKey(t *testing.T) {
	const secret = "channel-secret-1234"
	response := channelAccountResponse(&model.ChannelAccount{APIKey: secret}, nil)
	want := service.MaskCredential(secret)
	if response["api_key"] != want || response["masked_key"] != want {
		t.Fatalf("masked fields = %#v", response)
	}
	assertJSONDoesNotContainSecret(t, response, secret)
}

func TestGatewayKeyResponseMasksAPIKey(t *testing.T) {
	const secret = "gateway-secret-5678"
	response := newGwChannelKeyResponse(&model.GwChannelKey{APIKey: secret})
	want := service.MaskCredential(secret)
	if response.APIKey != want || response.MaskedKey != want {
		t.Fatalf("masked fields = %#v", response)
	}
	assertJSONDoesNotContainSecret(t, response, secret)
}

func TestCreateGatewayKeyRequestAcceptsAPIKey(t *testing.T) {
	const secret = "gateway-secret-9012"
	var request createGwChannelKeyRequest
	if err := json.Unmarshal([]byte(`{
		"channel_id": 7,
		"name": "primary",
		"api_key": "`+secret+`",
		"weight": 20,
		"status": 1,
		"max_conc": 3
	}`), &request); err != nil {
		t.Fatalf("unmarshal create request: %v", err)
	}

	key := request.model()
	if key.ChannelID != 7 || key.Name != "primary" || key.APIKey != secret ||
		key.Weight != 20 || key.Status != 1 || key.MaxConc != 3 {
		t.Fatalf("gateway key = %+v", key)
	}
	assertJSONDoesNotContainSecret(t, key, secret)
}

func assertJSONDoesNotContainSecret(t *testing.T, value any, secret string) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(body), secret) {
		t.Fatalf("response leaked secret: %s", body)
	}
}
