package migrate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCatalogPlanSanitizesAndValidatesGenericVideoSnapshot(t *testing.T) {
	config := `{
		"fixed_price":"8.5",
		"currency":"CNY",
		"adapter":{
			"auth_location":"header",
			"auth_key":"Authorization",
			"auth_prefix":"Bearer ",
			"submit":{"enabled":true,"method":"POST","path":"/tasks"},
			"poll":{"enabled":true,"method":"GET","path":"/tasks/{task_id}"},
			"request":{"fields":{"prompt":"prompt"},"params_mode":"merge_missing"},
			"service_tiers":{"priority":{"label":"Priority","surcharge_percent":"12.5","request_params":{"priority":true}}},
			"response":{
				"task_id_paths":["data.task_id"],
				"status_paths":["data.status"],
				"video_url_paths":["data.video_url"],
				"status_map":{"queued":"submitted","success":"completed","failed":"failed"},
				"submit_default_status":"submitted",
				"poll_default_status":"tracking",
				"unknown_status":"tracking"
			},
			"validation":{"models":{"vendor-video":{"duration_min":1,"duration_max":10}}}
		}
	}`
	snapshot := catalogTestSnapshot(
		catalogTestTable("video_channels", map[string]string{
			"id": "1", "name": "Generic video", "adapter_type": "generic", "base_url": "https://video.example.com",
			"status": "active", "priority": "9", "request_timeout_seconds": "30", "adapter_profile": "json_task_v1",
			"models":       `[{"model_name":"public-video","vendor_model":"vendor-video"}]`,
			"capabilities": `{}`, "result_storage_enabled": "0", "extra_config": config,
		}),
		catalogTestTable("video_channel_keys", map[string]string{
			"id": "11", "channel_id": "1", "api_key": "video-secret", "label": "primary",
			"weight": "2", "max_concurrency": "3", "status": "active",
		}),
	)

	plan := buildCatalogPlan(snapshot)
	if len(plan.issues) != 0 {
		t.Fatalf("unexpected catalog issues: %+v", plan.issues)
	}
	if len(plan.products) != 1 || len(plan.skus) != 1 {
		t.Fatalf("products=%d skus=%d", len(plan.products), len(plan.skus))
	}
	constraints := string(plan.products[0].Constraints)
	for _, forbidden := range []string{"fixed_price", "surcharge_percent", `"currency"`} {
		if strings.Contains(constraints, forbidden) {
			t.Fatalf("commercial field %q remains in executable config: %s", forbidden, constraints)
		}
	}
	for _, required := range []string{`"adapter"`, `"profile":"json_task_v1"`, `"request"`, `"response"`, `"request_params"`} {
		if !strings.Contains(constraints, required) {
			t.Fatalf("executable field %q was removed: %s", required, constraints)
		}
	}
}

func TestBuildCatalogPlanRejectsGenericVideoModelOutsideValidatedMatrix(t *testing.T) {
	snapshot := catalogTestSnapshot(
		catalogTestTable("video_channels", map[string]string{
			"id": "1", "name": "Generic video", "adapter_type": "generic", "base_url": "https://video.example.com",
			"status": "active", "priority": "1", "request_timeout_seconds": "30", "adapter_profile": "json_task_v1", "models": `["undeclared-model"]`,
			"capabilities": `{}`, "result_storage_enabled": "0",
			"extra_config": `{"adapter":{"submit":{"enabled":true,"method":"POST","path":"/tasks"},"poll":{"enabled":true,"method":"GET","path":"/tasks/{task_id}"},"request":{"fields":{"prompt":"prompt"}},"response":{"task_id_paths":["id"],"status_paths":["status"],"video_url_paths":["url"],"status_map":{"queued":"submitted"},"submit_default_status":"submitted","poll_default_status":"tracking","unknown_status":"tracking"},"validation":{"models":{"declared-model":{}}}}}`,
		}),
		catalogTestTable("video_channel_keys", map[string]string{
			"id": "2", "channel_id": "1", "api_key": "video-secret", "label": "primary", "weight": "1", "max_concurrency": "1", "status": "active",
		}),
	)

	plan := buildCatalogPlan(snapshot)
	if len(plan.products) != 0 || !catalogTestHasIssue(plan, "invalid_video_catalog_contract") {
		t.Fatalf("products=%d issues=%+v", len(plan.products), plan.issues)
	}
}

func TestBuildCatalogPlanSplitsMultiOperationEndpointSnapshot(t *testing.T) {
	snapshot := catalogTestSnapshot(
		catalogTestTable("channels", map[string]string{
			"id": "1", "type": "openai", "name": "Images", "base_url": "https://images.example.com",
			"callback_secret": "", "config": `{}`, "status": "active",
		}),
		catalogTestTable("channel_accounts", map[string]string{
			"id": "2", "channel_id": "1", "name": "primary", "api_key": "image-secret", "config": `{}`,
			"weight": "1", "status": "active", "max_tasks": "4",
		}),
		catalogTestTable("models", map[string]string{
			"code": "image-model", "name": "Image model", "type": "image", "protocol": "openai",
			"description": "", "features": `[]`, "aliases": `[]`, "sort": "1", "status": "active",
		}),
		catalogTestTable("endpoints", map[string]string{
			"id": "3", "model_code": "image-model", "route_operation": "images.generate",
			"supported_operations": `["images.generate","images.edit"]`, "channel_id": "1", "account_id": "0",
			"protocol": "openai", "request_path": "/v1/images/generations", "request_method": "POST",
			"content_type": "application/json", "auth_location": "header", "auth_key": "Authorization",
			"auth_value_prefix": "Bearer ", "vendor_model": "vendor-image", "interaction_mode": "sync",
			"supports_stream": "0", "param_mapping": `{}`, "param_schema": `{}`, "response_mapping": `{}`,
			"poll_path": "", "callback_mapping": `{}`, "extra_headers": `{}`, "extra_config": `{}`,
			"timeout": "120", "priority": "5", "status": "active",
		}),
		catalogTestTable("endpoint_accounts", map[string]string{
			"id": "4", "endpoint_id": "3", "account_id": "2", "status": "active", "priority": "7", "weight": "3",
		}),
	)

	plan := buildCatalogPlan(snapshot)
	if len(plan.issues) != 0 {
		t.Fatalf("unexpected catalog issues: %+v", plan.issues)
	}
	if len(plan.products) != 2 || len(plan.skus) != 2 {
		t.Fatalf("products=%d skus=%d", len(plan.products), len(plan.skus))
	}
	operations := map[string]bool{}
	for _, product := range plan.products {
		if len(product.SKUKeys) != 1 {
			t.Fatalf("product %s has %d SKUs", product.Code, len(product.SKUKeys))
		}
		operations[plan.skuByKey[product.SKUKeys[0]].Operation] = true
	}
	if !operations["images.generate"] || !operations["images.edit"] {
		t.Fatalf("split operations=%v", operations)
	}
	var endpointProducts, bindingOfferings int
	for _, mapping := range plan.mappings {
		if mapping.Row.Table == "endpoints" && mapping.Row.PrimaryKey == "3" && mapping.TargetType == "product" {
			endpointProducts++
		}
		if mapping.Row.Table == "endpoint_accounts" && mapping.Row.PrimaryKey == "4" && mapping.TargetType == "offering" {
			bindingOfferings++
		}
	}
	if endpointProducts != 2 || bindingOfferings != 2 {
		t.Fatalf("endpoint product mappings=%d binding offering mappings=%d", endpointProducts, bindingOfferings)
	}
}

func catalogTestSnapshot(tables ...runtimeSourceTable) catalogSnapshot {
	snapshot := catalogSnapshot{Tables: make(map[string]runtimeSourceTable, len(tables))}
	for _, table := range tables {
		snapshot.Tables[table.Spec.Name] = table
		snapshot.Rows += int64(len(table.Rows))
	}
	return snapshot
}

func catalogTestTable(name string, values ...map[string]string) runtimeSourceTable {
	var spec runtimeSourceSpec
	for _, candidate := range catalogSourceSpecs {
		if candidate.Name == name {
			spec = candidate
			break
		}
	}
	if spec.Name == "" {
		panic("unknown catalog source table: " + name)
	}
	table := runtimeSourceTable{Spec: spec, Present: true, Columns: make(map[string]runtimeColumn, len(spec.Raw))}
	for index, name := range spec.Raw {
		table.Columns[name] = runtimeColumn{Name: name, Ordinal: index + 1}
	}
	for _, fields := range values {
		row := runtimeSourceRow{Table: spec.Name, PrimaryKey: fields[spec.PrimaryKey], Revision: "test-revision", Values: make(map[string]runtimeValue, len(fields))}
		for name, value := range fields {
			row.Values[name] = runtimeValue{Bytes: []byte(value), Valid: true}
		}
		if row.PrimaryKey == "" {
			encoded, _ := json.Marshal(fields)
			panic("catalog test row has no primary key: " + string(encoded))
		}
		table.Rows = append(table.Rows, row)
	}
	return table
}

func catalogTestHasIssue(plan *catalogPlan, code string) bool {
	for _, issue := range plan.issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
