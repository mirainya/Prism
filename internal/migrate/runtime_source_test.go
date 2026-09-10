package migrate

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/mirainya/Prism/internal/gateway/security"
)

func TestLoadRuntimeSourceTableHashesLargeFieldsInSQL(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	spec := runtimeSourceSpec{
		Name: "tasks", PrimaryKey: "id",
		Raw: []string{"id"}, Hashed: []string{"request_params"}, Preview: []string{"prompt"},
	}
	mock.ExpectQuery("SELECT column_name,column_type,is_nullable,ordinal_position").
		WithArgs("tasks").
		WillReturnRows(sqlmock.NewRows([]string{"column_name", "column_type", "is_nullable", "ordinal_position"}).
			AddRow("id", "bigint unsigned", "NO", 1).
			AddRow("request_params", "longtext", "YES", 2).
			AddRow("prompt", "text", "YES", 3))
	digest := strings.Repeat("a", 64)
	mock.ExpectQuery("SELECT .*SHA2.* FROM `tasks` ORDER BY `id`").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "request_params__present", "request_params__bytes", "request_params__sha256",
			"prompt__present", "prompt__bytes", "prompt__sha256", "prompt__preview",
		}).AddRow("17", "1", "10485760", digest, "1", "5", digest, "hello"))

	table, err := loadRuntimeSourceTable(context.Background(), db, spec, bytes.Repeat([]byte{7}, security.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	if !table.Present || len(table.Rows) != 1 {
		t.Fatalf("unexpected source table: %+v", table)
	}
	row := table.Rows[0]
	if _, copied := row.Values["request_params"]; copied {
		t.Fatal("large request body must not be selected into process memory")
	}
	if row.contentLength("request_params") != 10485760 || row.contentDigest("request_params") != digest || row.preview("prompt") != "hello" {
		t.Fatalf("unexpected derived evidence: %+v", row.Values)
	}
	if len(row.Revision) != 64 {
		t.Fatalf("revision length=%d", len(row.Revision))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSnapshotHMACIsStableAndDetectsChanges(t *testing.T) {
	key := bytes.Repeat([]byte{9}, security.KeySize)
	row := runtimeSourceRow{Table: "api_calls", PrimaryKey: "call-1", Values: map[string]runtimeValue{
		"id": {Bytes: []byte("call-1"), Valid: true},
	}}
	row.Revision = runtimeRowHMAC(row, key)
	tables := map[string]runtimeSourceTable{
		"api_calls": {
			Spec: runtimeSourceSpec{Name: "api_calls", PrimaryKey: "id"}, Present: true,
			Columns: map[string]runtimeColumn{"id": {Name: "id", Type: "varchar(64)", Nullable: "NO", Ordinal: 1}},
			Rows:    []runtimeSourceRow{row},
		},
	}
	first := runtimeSnapshotHMAC(tables, key)
	second := runtimeSnapshotHMAC(tables, key)
	if first != second || len(first) != 64 {
		t.Fatalf("snapshot HMAC is not stable: %q %q", first, second)
	}
	changed := tables["api_calls"]
	changed.Columns["id"] = runtimeColumn{Name: "id", Type: "varchar(128)", Nullable: "NO", Ordinal: 1}
	tables["api_calls"] = changed
	if runtimeSnapshotHMAC(tables, key) == first {
		t.Fatal("schema changes must change the source revision")
	}
}

func TestSafeRuntimePreviewRejectsInlineBinary(t *testing.T) {
	if got := safeRuntimePreview("data:image/png;base64,AAAA"); got != "" {
		t.Fatalf("data URI leaked into preview: %q", got)
	}
	if got := safeRuntimePreview(strings.Repeat("A", 300)); got != "" {
		t.Fatalf("base64-like run leaked into preview: %q", got)
	}
	if got := safeRuntimePreview("a normal prompt"); got != "a normal prompt" {
		t.Fatalf("ordinary prompt changed: %q", got)
	}
}

func TestRuntimePublicIDDeterministicForLongSourceIdentity(t *testing.T) {
	key := bytes.Repeat([]byte{11}, security.KeySize)
	source := strings.Repeat("x", 80)
	first := runtimePublicID("call", source, key)
	if len(first) != 36 || first != runtimePublicID("call", source, key) {
		t.Fatalf("unexpected stable public ID %q", first)
	}
	if first == runtimePublicID("response", source, key) {
		t.Fatal("public ID namespaces must be isolated")
	}
}
