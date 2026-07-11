package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPClientExecutesPreparedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if string(body) != "payload" {
			t.Fatalf("body = %q", body)
		}
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("ok"))
	}))
	defer server.Close()

	client := HTTPClient{}
	response, err := client.Do(context.Background(), PreparedRequest{Method: http.MethodPost, URL: server.URL, Body: []byte("payload")})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, err := client.ReadBody(response)
	if err != nil || string(body) != "ok" {
		t.Fatalf("body = %q, err = %v", body, err)
	}
}
