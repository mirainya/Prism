package payloadview

import "testing"

func TestExtractFailureMessage(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "provider message", body: `{"code":"insufficient_user_quota","message":"余额不足","data":null}`, want: "余额不足"},
		{name: "nested error", body: `{"error":{"message":"invalid model"}}`, want: "invalid model"},
		{name: "plain text", body: `upstream unavailable`, want: "upstream unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ExtractFailureMessage([]byte(test.body)); got != test.want {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
		})
	}
}
