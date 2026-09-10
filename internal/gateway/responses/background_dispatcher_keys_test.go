package responses

import (
	"bytes"
	"testing"

	"github.com/mirainya/Prism/internal/gateway/engine"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
)

func TestNewBackgroundDispatcherCopiesKeys(t *testing.T) {
	keys := gatewayruntime.AsyncKeys{
		CredentialKEK:  bytes.Repeat([]byte{1}, 32),
		CredentialHMAC: bytes.Repeat([]byte{2}, 32),
		PayloadKEK:     bytes.Repeat([]byte{3}, 32),
		PayloadHMAC:    bytes.Repeat([]byte{4}, 32),
	}
	dispatcher, err := NewBackgroundDispatcher(&gatewayruntime.Service{}, &engine.Engine{}, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		clear(dispatcher.keys.CredentialKEK)
		clear(dispatcher.keys.CredentialHMAC)
		clear(dispatcher.keys.PayloadKEK)
		clear(dispatcher.keys.PayloadHMAC)
	}()

	clear(keys.CredentialKEK)
	clear(keys.CredentialHMAC)
	clear(keys.PayloadKEK)
	clear(keys.PayloadHMAC)

	for name, check := range map[string]struct {
		got  []byte
		want byte
	}{
		"credential KEK":  {dispatcher.keys.CredentialKEK, 1},
		"credential HMAC": {dispatcher.keys.CredentialHMAC, 2},
		"payload KEK":     {dispatcher.keys.PayloadKEK, 3},
		"payload HMAC":    {dispatcher.keys.PayloadHMAC, 4},
	} {
		if !bytes.Equal(check.got, bytes.Repeat([]byte{check.want}, 32)) {
			t.Fatalf("%s was not copied", name)
		}
	}
}
