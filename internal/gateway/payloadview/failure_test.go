package payloadview

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func TestExtractFailureMessage(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "provider message", body: `{"code":"insufficient_user_quota","message":"余额不足","data":null}`, want: "余额不足"},
		{name: "nested error", body: `{"error":{"message":"invalid model"}}`, want: "invalid model"},
		{name: "nested object is not public", body: `{"error":{"code":"bad_request","trace":"internal"}}`, want: ""},
		{name: "json string", body: `"upstream unavailable"`, want: "upstream unavailable"},
		{name: "successful payload", body: `{"data":[{"url":"https://example.com/result.png"}]}`, want: ""},
		{name: "plain text", body: `upstream unavailable`, want: "upstream unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ExtractFailureMessage([]byte(test.body)); got != test.want {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSanitizeFailureMessageRedactsSensitiveValues(t *testing.T) {
	message := "Authorization: Bearer secret-token password=hunter2 key=sk-1234567890abcdef " +
		"key=short-secret client_secret=client-value private_key=private-value " +
		"xfs_test_1234567890abcdef https://internal.example/path?q=secret 10.0.0.8:8080 " +
		"eyJhbGciOiJIUzI1NiJ9.abcdefghijklmnopqrstuvwxyz.signature"
	got := SanitizeFailureMessage(message)
	for _, sensitive := range []string{
		"secret-token", "hunter2", "sk-1234567890abcdef", "xfs_test_1234567890abcdef",
		"short-secret", "client-value", "private-value", "internal.example", "10.0.0.8", "eyJhbGciOiJIUzI1NiJ9",
	} {
		if strings.Contains(got, sensitive) {
			t.Fatalf("sanitized message contains %q: %s", sensitive, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "[URL]") || !strings.Contains(got, "[HOST]") {
		t.Fatalf("sanitized message = %q", got)
	}
}

func TestEncodeFailureDiagnosticSanitizesProviderMessage(t *testing.T) {
	status := uint16(422)
	diagnostic, err := EncodeFailureDiagnostic(
		"bad_image",
		"request https://private.example/task failed; Authorization: Bearer secret-token; password=hunter2; host 10.0.0.8:8080",
		&status,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"private.example", "secret-token", "hunter2", "10.0.0.8"} {
		if strings.Contains(string(diagnostic), sensitive) {
			t.Fatalf("diagnostic contains %q: %s", sensitive, diagnostic)
		}
	}
	var decoded requestFailureDiagnostic
	if err := json.Unmarshal(diagnostic, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 1 || decoded.ProviderCode != "bad_image" || decoded.ProviderHTTPStatus != status {
		t.Fatalf("diagnostic metadata = %+v", decoded)
	}
	for _, marker := range []string{"[URL]", "[REDACTED]", "[HOST]"} {
		if !strings.Contains(decoded.ProviderMessage, marker) {
			t.Fatalf("provider message %q does not contain %q", decoded.ProviderMessage, marker)
		}
	}
}

func TestExtractFailureMessageTruncatesUnicodeByRunes(t *testing.T) {
	message := strings.Repeat("错", maxFailureMessageRunes+10)
	got := ExtractFailureMessage([]byte(`{"message":"` + message + `"}`))
	if utf8.RuneCountInString(got) != maxFailureMessageRunes {
		t.Fatalf("message rune count = %d, want %d", utf8.RuneCountInString(got), maxFailureMessageRunes)
	}
}

func TestReadLatestRequestFailureFailsClosedWhenDiagnosticCannotBeRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := repository.New(db)
	if err != nil {
		t.Fatal(err)
	}

	mock.ExpectQuery("SELECT l.id,l.error_code,l.http_status,l.response_payload_blob_id,l.diagnostic_blob_id").
		WithArgs(uint64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "error_code", "http_status", "response_payload_blob_id", "diagnostic_blob_id"}).
			AddRow(uint64(31), "provider_task_failed", 200, uint64(44), uint64(45)))
	mock.ExpectQuery("SELECT b.keyring_id,b.purpose,b.schema_version").
		WithArgs(uint64(45)).
		WillReturnError(errors.New("diagnostic unavailable"))

	failure, err := ReadLatestRequestFailure(context.Background(), store, 10)
	if err != nil {
		t.Fatal(err)
	}
	if failure.Code != "provider_task_failed" || failure.HTTPStatus != 200 || failure.Message != "上游任务失败" {
		t.Fatalf("failure = %+v", failure)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
