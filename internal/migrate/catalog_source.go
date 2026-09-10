package migrate

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"sort"
)

const catalogImportOperation = "legacy_catalog_import"
const catalogSnapshotMappingTable = "legacy_catalog_snapshot"

// Catalog import reads a fixed allow-list. Secret columns are needed only to
// build encrypted credential versions and are never copied into catalog JSON.
var catalogSourceSpecs = []runtimeSourceSpec{
	{Name: "gw_channels", PrimaryKey: "id", Raw: []string{
		"id", "name", "protocol", "base_url", "extra_headers", "config", "status", "sort", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "gw_channel_keys", PrimaryKey: "id", Raw: []string{
		"id", "channel_id", "name", "api_key", "weight", "status", "max_conc", "current_conc", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "gw_abilities", PrimaryKey: "id", Raw: []string{
		"id", "model_name", "channel_id", "key_id", "vendor_model", "priority", "price_mode", "input_price", "output_price", "capabilities", "status", "created_at", "updated_at",
	}},
	{Name: "gw_ability_transports", PrimaryKey: "id", Raw: []string{
		"id", "ability_id", "transport", "status", "config", "checked_at", "last_error", "created_at", "updated_at",
	}},
	{Name: "gw_model_meta", PrimaryKey: "model_name", Raw: []string{
		"model_name", "display_name", "thinking_config", "max_tokens", "features", "group_name", "status", "sort", "updated_at",
	}},
	{Name: "channels", PrimaryKey: "id", Raw: []string{
		"id", "type", "name", "base_url", "callback_secret", "config", "status", "sort", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "channel_accounts", PrimaryKey: "id", Raw: []string{
		"id", "channel_id", "name", "api_key", "config", "weight", "status", "max_tasks", "current_tasks", "supported_models", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "models", PrimaryKey: "code", Raw: []string{
		"code", "name", "type", "provider", "protocol", "description", "features", "aliases", "param_schema", "max_tokens", "thinking_config", "sort", "status", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "account_models", PrimaryKey: "id", Raw: []string{
		"id", "account_id", "model_code", "vendor_model", "priority", "status", "price_mode", "input_price", "output_price", "created_at", "updated_at",
	}},
	{Name: "endpoints", PrimaryKey: "id", Raw: []string{
		"id", "model_code", "route_operation", "supported_operations", "channel_id", "account_id", "origin_type", "origin_account_id", "origin_snapshot", "discovered_at",
		"protocol", "request_path", "request_method", "content_type", "auth_location", "auth_key", "auth_value_prefix", "vendor_model", "interaction_mode", "supports_stream", "default_stream",
		"price_mode", "input_price", "output_price", "param_mapping", "param_schema", "response_mapping", "poll_path", "poll_method", "poll_interval", "poll_max_attempts", "poll_param_mapping", "poll_response_mapping", "callback_mapping", "extra_headers", "extra_config", "timeout", "priority", "status", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "endpoint_accounts", PrimaryKey: "id", Raw: []string{
		"id", "endpoint_id", "account_id", "status", "priority", "weight", "created_at", "updated_at",
	}},
	{Name: "endpoint_adapters", PrimaryKey: "id", Raw: []string{
		"id", "endpoint_id", "code", "active_revision_id", "status", "config", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "endpoint_adapter_revisions", PrimaryKey: "id", Raw: []string{
		"id", "adapter_id", "version", "digest", "config", "created_by", "created_at", "updated_at", "deleted_at",
	}},
	{Name: "video_channels", PrimaryKey: "id", Raw: []string{
		"id", "name", "adapter_type", "adapter_profile", "base_url", "status", "priority", "request_timeout_seconds", "models", "capabilities", "supports_first_frame", "supports_last_frame", "supports_audio", "supports_web_search", "cancel_mode", "pricing", "pricing_mode", "fixed_price", "markup_ratio", "asset_resolver", "result_storage_enabled", "extra_config", "created_at", "updated_at",
	}},
	{Name: "video_channel_keys", PrimaryKey: "id", Raw: []string{
		"id", "channel_id", "api_key", "label", "weight", "max_concurrency", "status", "current_concurrency", "total_calls", "last_used_at", "created_at", "consecutive_failures",
	}},
}

type catalogSnapshot struct {
	Tables map[string]runtimeSourceTable
	HMAC   string
	Rows   int64
}

func loadCatalogSnapshot(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, hmacKey []byte) (catalogSnapshot, error) {
	snapshot := catalogSnapshot{Tables: make(map[string]runtimeSourceTable, len(catalogSourceSpecs))}
	for _, spec := range catalogSourceSpecs {
		table, err := loadRuntimeSourceTable(ctx, q, spec, hmacKey)
		if err != nil {
			return catalogSnapshot{}, err
		}
		for index := range table.Rows {
			table.Rows[index].Revision = catalogRowHMAC(table.Rows[index], hmacKey)
		}
		snapshot.Tables[spec.Name] = table
		snapshot.Rows += int64(len(table.Rows))
	}
	snapshot.HMAC = catalogSnapshotDigest(snapshot.Tables, hmacKey)
	return snapshot, nil
}

func catalogRowHMAC(row runtimeSourceRow, key []byte) string {
	mac := hmac.New(sha256.New, key)
	writeRuntimeDigestField(mac, []byte("prism-legacy-catalog-row-v1"))
	writeRuntimeDigestField(mac, []byte(row.Table))
	names := make([]string, 0, len(row.Values))
	for name := range row.Values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
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

func catalogSnapshotDigest(tables map[string]runtimeSourceTable, key []byte) string {
	mac := hmac.New(sha256.New, key)
	writeRuntimeDigestField(mac, []byte("prism-legacy-catalog-snapshot-v1"))
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
