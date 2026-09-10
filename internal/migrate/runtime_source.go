package migrate

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Runtime history is read through a fixed allow-list. Large JSON/body columns
// are reduced to length and SHA-256 inside MySQL, so an old inline base64 image
// is never copied through the importer or written to the unified schema.
type runtimeSourceSpec struct {
	Name       string
	PrimaryKey string
	Raw        []string
	Hashed     []string
	Preview    []string
}

type runtimeColumn struct {
	Name, Type, Nullable string
	Ordinal              int
}

type runtimeValue struct {
	Bytes []byte
	Valid bool
}

func (v runtimeValue) String() string {
	if !v.Valid {
		return ""
	}
	return string(v.Bytes)
}

type runtimeSourceRow struct {
	Table, PrimaryKey, Revision string
	Values                      map[string]runtimeValue
}

func (r runtimeSourceRow) value(name string) runtimeValue { return r.Values[name] }
func (r runtimeSourceRow) text(name string) string        { return strings.TrimSpace(r.value(name).String()) }
func (r runtimeSourceRow) present(name string) bool {
	v := r.value(name + "__present")
	return v.Valid && v.String() == "1"
}
func (r runtimeSourceRow) contentDigest(name string) string {
	return strings.ToLower(r.text(name + "__sha256"))
}
func (r runtimeSourceRow) contentLength(name string) uint64 {
	v, _ := strconv.ParseUint(r.text(name+"__bytes"), 10, 64)
	return v
}
func (r runtimeSourceRow) preview(name string) string { return r.value(name + "__preview").String() }

type runtimeSourceTable struct {
	Spec    runtimeSourceSpec
	Present bool
	Columns map[string]runtimeColumn
	Rows    []runtimeSourceRow
}

func (t runtimeSourceTable) missingColumns(names ...string) []string {
	var missing []string
	for _, name := range names {
		if _, ok := t.Columns[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

type runtimeSnapshot struct {
	Tables map[string]runtimeSourceTable
	HMAC   string
	Rows   int64
}

var runtimeSourceSpecs = []runtimeSourceSpec{
	{
		Name: "users", PrimaryKey: "id",
		Raw: []string{"id", "balance", "status", "deleted_at", "created_at", "updated_at"},
	},
	{
		Name: "tokens", PrimaryKey: "id",
		Raw: []string{"id", "user_id", "balance", "total_used", "status", "deleted_at", "created_at", "updated_at"},
	},
	{
		Name: "api_calls", PrimaryKey: "id",
		Raw:    []string{"id", "request_id", "user_id", "token_id", "endpoint", "operation", "model", "status", "is_stream", "background", "store", "retain_payload", "payload_expires_at", "resource_type", "resource_id", "conversation_id", "project_conversation", "final_attempt_id", "attempt_count", "input_tokens", "output_tokens", "total_tokens", "cached_input_tokens", "reasoning_output_tokens", "reserved_amount", "final_cost", "refunded_amount", "http_status", "error_type", "error_code", "error_retryable", "started_at", "first_byte_at", "completed_at", "duration_ms", "ttft_ms", "client_disconnected", "created_at", "updated_at"},
		Hashed: []string{"usage_json", "error_message", "error_param"},
	},
	{
		Name: "api_call_attempts", PrimaryKey: "id",
		Raw:    []string{"id", "call_id", "attempt_no", "route_kind", "stage", "ability_id", "channel_id", "key_id", "endpoint_id", "account_id", "protocol", "vendor_model", "transport", "request_path", "status", "http_status", "error_type", "error_code", "error_retryable", "input_tokens", "output_tokens", "total_tokens", "cached_input_tokens", "reasoning_output_tokens", "duration_ms", "ttft_ms", "provider_response_id", "started_at", "first_byte_at", "completed_at", "created_at", "updated_at"},
		Hashed: []string{"usage_json", "error_message"},
	},
	{
		Name: "api_call_payloads", PrimaryKey: "id",
		Raw:    []string{"id", "call_id", "attempt_id", "kind", "content_type", "encrypted", "truncated", "original_bytes", "expires_at", "created_at", "updated_at"},
		Hashed: []string{"data"},
	},
	{
		Name: "tasks", PrimaryKey: "id",
		Raw:    []string{"id", "task_no", "call_id", "user_id", "token_id", "model_code", "route_operation", "channel_id", "endpoint_id", "account_id", "vendor_task_id", "status", "progress", "callback_status", "callback_attempts", "cost", "refunded", "started_at", "completed_at", "created_at", "updated_at"},
		Hashed: []string{"request_params", "mapped_params", "submit_checkpoint", "endpoint_snapshot", "vendor_response", "result", "error_message", "callback_url"},
	},
	{
		Name: "video_tasks", PrimaryKey: "id",
		Raw:     []string{"id", "call_id", "user_id", "token_id", "model", "vendor_model", "status", "progress", "task_mode", "service_tier", "resolution", "ratio", "duration", "generate_audio", "channel_id", "key_id", "adapter_type", "provider_task_id", "estimated_cost", "markup_ratio", "final_cost", "billing_status", "created_at", "submitted_at", "completed_at"},
		Hashed:  []string{"content_json", "params_json", "route_plan", "provider_response", "provider_metadata", "result_json", "error_message", "submit_checkpoint", "callback_url"},
		Preview: []string{"prompt"},
	},
	{
		Name: "ai_responses", PrimaryKey: "id",
		Raw:    []string{"id", "user_id", "token_id", "call_id", "model", "status", "background", "store", "previous_response_id", "provider_response_id", "channel_id", "key_id", "upstream_transport", "request_log_id", "request_hash", "execution_attempt", "result_ready_at", "created_at", "completed_at"},
		Hashed: []string{"request_json", "input_items", "output_items", "response_json", "usage_json", "metadata", "error_json", "idempotency_key"},
	},
	{
		Name: "channel_request_logs", PrimaryKey: "id",
		Raw:    []string{"id", "call_id", "attempt_id", "task_id", "task_no", "conversation_id", "channel_id", "account_id", "capability_code", "request_type", "is_stream", "model_code", "vendor_model", "upstream_transport", "request_path", "finish_reason", "usage_prompt_tokens", "usage_completion_tokens", "usage_total_tokens", "method", "status_code", "duration_ms", "request_at", "created_at", "updated_at"},
		Hashed: []string{"url", "request_headers", "request_body", "response_body", "response_preview", "error_message"},
	},
	{
		// request_logs was used by early installations. It is accepted only
		// when channel_request_logs is absent or empty.
		Name: "request_logs", PrimaryKey: "id",
		Raw:    []string{"id", "call_id", "attempt_id", "task_id", "task_no", "conversation_id", "channel_id", "account_id", "capability_code", "request_type", "is_stream", "model_code", "vendor_model", "upstream_transport", "request_path", "finish_reason", "usage_prompt_tokens", "usage_completion_tokens", "usage_total_tokens", "method", "status_code", "duration_ms", "request_at", "created_at", "updated_at"},
		Hashed: []string{"url", "request_headers", "request_body", "response_body", "response_preview", "error_message"},
	},
	{
		Name: "billing_logs", PrimaryKey: "id",
		Raw:    []string{"id", "idempotent_key", "token_id", "user_id", "call_id", "attempt_id", "phase", "amount", "type", "status", "created_at", "updated_at"},
		Hashed: []string{"pricing_snapshot", "remark"},
	},
	{
		Name: "balance_entries", PrimaryKey: "id",
		Raw:    []string{"id", "entry_key", "source_key", "account_type", "account_id", "user_id", "token_id", "direction", "category", "amount", "balance_before", "balance_after", "call_id", "attempt_id", "actor_user_id", "created_at"},
		Hashed: []string{"metadata"},
	},
}

func loadRuntimeSnapshot(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, hmacKey []byte) (runtimeSnapshot, error) {
	snapshot := runtimeSnapshot{Tables: make(map[string]runtimeSourceTable, len(runtimeSourceSpecs))}
	for _, spec := range runtimeSourceSpecs {
		table, err := loadRuntimeSourceTable(ctx, q, spec, hmacKey)
		if err != nil {
			return runtimeSnapshot{}, err
		}
		snapshot.Tables[spec.Name] = table
		snapshot.Rows += int64(len(table.Rows))
	}
	snapshot.HMAC = runtimeSnapshotHMAC(snapshot.Tables, hmacKey)
	return snapshot, nil
}

func loadRuntimeSourceTable(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, spec runtimeSourceSpec, hmacKey []byte) (runtimeSourceTable, error) {
	table := runtimeSourceTable{Spec: spec, Columns: map[string]runtimeColumn{}}
	rows, err := q.QueryContext(ctx, `SELECT column_name,column_type,is_nullable,ordinal_position
		FROM information_schema.columns
		WHERE table_schema=DATABASE() AND table_name=? ORDER BY ordinal_position`, spec.Name)
	if err != nil {
		return table, fmt.Errorf("probe legacy table %s: %w", spec.Name, err)
	}
	for rows.Next() {
		var column runtimeColumn
		if err := rows.Scan(&column.Name, &column.Type, &column.Nullable, &column.Ordinal); err != nil {
			rows.Close()
			return table, err
		}
		table.Columns[column.Name] = column
	}
	if err := rows.Close(); err != nil {
		return table, err
	}
	if err := rows.Err(); err != nil {
		return table, err
	}
	if len(table.Columns) == 0 {
		return table, nil
	}
	table.Present = true
	if _, ok := table.Columns[spec.PrimaryKey]; !ok {
		return table, nil
	}

	type selection struct{ key, expression string }
	selected := make([]selection, 0, len(spec.Raw)+len(spec.Hashed)*3+len(spec.Preview)*4)
	seen := map[string]bool{}
	add := func(key, expression string) {
		if !seen[key] {
			selected = append(selected, selection{key: key, expression: expression})
			seen[key] = true
		}
	}
	for _, name := range spec.Raw {
		if _, ok := table.Columns[name]; ok {
			add(name, quoteRuntimeIdentifier(name))
		}
	}
	for _, name := range append(append([]string{}, spec.Hashed...), spec.Preview...) {
		if _, ok := table.Columns[name]; !ok {
			continue
		}
		quoted := quoteRuntimeIdentifier(name)
		add(name+"__present", "CASE WHEN "+quoted+" IS NULL THEN 0 ELSE 1 END")
		add(name+"__bytes", "CASE WHEN "+quoted+" IS NULL THEN 0 ELSE OCTET_LENGTH("+quoted+") END")
		add(name+"__sha256", "CASE WHEN "+quoted+" IS NULL THEN NULL ELSE LOWER(SHA2(CAST("+quoted+" AS BINARY),256)) END")
	}
	for _, name := range spec.Preview {
		if _, ok := table.Columns[name]; ok {
			add(name+"__preview", "LEFT(CAST("+quoteRuntimeIdentifier(name)+" AS CHAR),2000)")
		}
	}
	if len(selected) == 0 {
		return table, nil
	}
	expressions := make([]string, len(selected))
	for index, value := range selected {
		expressions[index] = value.expression + " AS " + quoteRuntimeIdentifier(value.key)
	}
	query := "SELECT " + strings.Join(expressions, ",") + " FROM " + quoteRuntimeIdentifier(spec.Name) + " ORDER BY " + quoteRuntimeIdentifier(spec.PrimaryKey)
	rows, err = q.QueryContext(ctx, query)
	if err != nil {
		return table, fmt.Errorf("read legacy table %s: %w", spec.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		raw := make([]sql.RawBytes, len(selected))
		destinations := make([]any, len(selected))
		for index := range raw {
			destinations[index] = &raw[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return table, err
		}
		values := make(map[string]runtimeValue, len(selected))
		for index, item := range selected {
			if raw[index] == nil {
				values[item.key] = runtimeValue{}
				continue
			}
			copyOfValue := append([]byte(nil), raw[index]...)
			values[item.key] = runtimeValue{Bytes: copyOfValue, Valid: true}
		}
		primaryKey := strings.TrimSpace(values[spec.PrimaryKey].String())
		row := runtimeSourceRow{Table: spec.Name, PrimaryKey: primaryKey, Values: values}
		row.Revision = runtimeRowHMAC(row, hmacKey)
		table.Rows = append(table.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return table, err
	}
	return table, nil
}

func quoteRuntimeIdentifier(name string) string {
	for _, char := range name {
		if char != '_' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			panic("unsafe runtime migration identifier: " + name)
		}
	}
	return "`" + name + "`"
}

func runtimeRowHMAC(row runtimeSourceRow, key []byte) string {
	mac := hmac.New(sha256.New, key)
	writeRuntimeDigestField(mac, []byte("prism-legacy-runtime-row-v1"))
	writeRuntimeDigestField(mac, []byte(row.Table))
	keys := make([]string, 0, len(row.Values))
	for name := range row.Values {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		writeRuntimeDigestField(mac, []byte(name))
		value := row.Values[name]
		if value.Valid {
			writeRuntimeDigestField(mac, []byte{1})
			writeRuntimeDigestField(mac, value.Bytes)
		} else {
			writeRuntimeDigestField(mac, []byte{0})
		}
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func runtimeSnapshotHMAC(tables map[string]runtimeSourceTable, key []byte) string {
	mac := hmac.New(sha256.New, key)
	writeRuntimeDigestField(mac, []byte("prism-legacy-runtime-snapshot-v1"))
	names := make([]string, 0, len(tables))
	for name := range tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		table := tables[name]
		writeRuntimeDigestField(mac, []byte(name))
		if table.Present {
			writeRuntimeDigestField(mac, []byte{1})
		} else {
			writeRuntimeDigestField(mac, []byte{0})
		}
		columns := make([]runtimeColumn, 0, len(table.Columns))
		for _, column := range table.Columns {
			columns = append(columns, column)
		}
		sort.Slice(columns, func(i, j int) bool { return columns[i].Ordinal < columns[j].Ordinal })
		for _, column := range columns {
			writeRuntimeDigestField(mac, []byte(column.Name))
			writeRuntimeDigestField(mac, []byte(column.Type))
			writeRuntimeDigestField(mac, []byte(column.Nullable))
		}
		for _, row := range table.Rows {
			writeRuntimeDigestField(mac, []byte(row.PrimaryKey))
			writeRuntimeDigestField(mac, []byte(row.Revision))
		}
	}
	return hex.EncodeToString(mac.Sum(nil))
}

type runtimeDigestWriter interface{ Write([]byte) (int, error) }

func writeRuntimeDigestField(writer runtimeDigestWriter, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write(value)
}

func runtimeUint(row runtimeSourceRow, name string) (uint64, error) {
	value := row.text(name)
	if value == "" {
		return 0, fmt.Errorf("%s is empty", name)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s is invalid", name)
	}
	return parsed, nil
}

func runtimeBool(row runtimeSourceRow, name string) bool {
	switch strings.ToLower(row.text(name)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func runtimeTime(row runtimeSourceRow, names ...string) (time.Time, bool) {
	for _, name := range names {
		value := row.text(name)
		if value == "" {
			continue
		}
		for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05.999", "2006-01-02 15:04:05", time.RFC3339Nano} {
			if parsed, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
				return parsed.UTC(), true
			}
		}
	}
	return time.Time{}, false
}
