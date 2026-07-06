package provider

import "testing"

func TestExtractUpstreamErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai style", `{"error":{"message":"invalid api key","type":"auth"}}`, "invalid api key"},
		{"error msg field", `{"error":{"msg":"rate limited"}}`, "rate limited"},
		{"top level message", `{"message":"bad request"}`, "bad request"},
		{"top level msg", `{"msg":"quota exceeded"}`, "quota exceeded"},
		{"error string", `{"error":"forbidden"}`, "forbidden"},
		{"detail field", `{"detail":"not found"}`, "not found"},
		{"priority error.message over message", `{"error":{"message":"deep"},"message":"shallow"}`, "deep"},
		{"non-json fallback", `internal server error`, "internal server error"},
		{"json without known field", `{"code":500,"data":null}`, `{"code":500,"data":null}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractUpstreamErrorMessage([]byte(c.body)); got != c.want {
				t.Errorf("extractUpstreamErrorMessage(%q) = %q, want %q", c.body, got, c.want)
			}
		})
	}
}

func TestExtractUpstreamErrorMessageTruncates(t *testing.T) {
	long := make([]byte, 600)
	for i := range long {
		long[i] = 'x'
	}
	got := extractUpstreamErrorMessage(long)
	if len(got) != 512 {
		t.Errorf("truncated length = %d, want 512", len(got))
	}
}
