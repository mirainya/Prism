package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/adapter"
	"github.com/mirainya/Prism/internal/video"
)

type catalogPlan struct {
	issues      []catalogIssue
	channels    []*catalogPlanChannel
	credentials []*catalogPlanCredential
	models      []*catalogPlanModel
	skus        []*catalogPlanSKU
	products    []*catalogPlanProduct
	mappings    []catalogPlanMapping

	channelByKey    map[string]*catalogPlanChannel
	credentialByKey map[string]*catalogPlanCredential
	modelByKey      map[string]*catalogPlanModel
	skuByKey        map[string]*catalogPlanSKU
	productByKey    map[string]*catalogPlanProduct
	modelSourceType map[string]string
	rowHandled      map[string]bool
	issueKeys       map[string]bool
	mappingKeys     map[string]bool
	apiNameOwners   map[string]string
	secretOwners    map[string]string
}

type catalogPlanChannel struct {
	Key, Code, Name, BaseURL, Status string
	Row                              runtimeSourceRow
}

type catalogPlanCredential struct {
	Key, ChannelKey, PoolCode, Code, Name, Status string
	Secret                                        []byte
	Weight                                        uint64
	RequestLimit, TaskLimit                       *uint64
	Row                                           runtimeSourceRow
	Config                                        json.RawMessage
}

type catalogPlanModel struct {
	Key, Code, DisplayName, Description, Visibility string
	Names, Tags                                     []string
	Sort                                            int
	MetadataPriority                                int
}

type catalogPlanSKU struct {
	Key, ModelKey, Operation, Method, Route, Code string
	DeliveryMode, IdempotencyMode                 string
	MaxResults                                    uint32
	ServiceTiers                                  []string
}

type catalogPlanAction struct {
	Code, AllowedSourceState, IdempotencyMode string
}

type catalogAllowedHost struct {
	Protocol, Host string
	Port           uint16
}

type catalogPlanProduct struct {
	Key, ChannelKey, CredentialKey, ModelKey, Code, VendorModel string
	Adapter                                                     adapter.Descriptor
	TransportCode, BaseURL, Protocol, Method, Path, AuthScheme  string
	TaskScope, CancelMode, SourceURLPolicy                      string
	UpstreamScopeKind, UpstreamScopeKey                         string
	DeliveryMode, CostPlanCode                                  string
	TimeoutMS                                                   uint64
	Priority                                                    uint32
	Weight                                                      uint64
	Active                                                      bool
	Constraints                                                 json.RawMessage
	AllowedHosts                                                []catalogAllowedHost
	Actions                                                     []catalogPlanAction
	SKUKeys                                                     []string
	Row                                                         runtimeSourceRow
}

type catalogPlanMapping struct {
	Row, TargetRow        runtimeSourceRow
	TargetType, TargetKey string
	TargetDiscriminator   string
}

type catalogOperation struct {
	Code, Method, Route string
	MaxResults          uint32
}

var catalogOperations = map[string]catalogOperation{
	"chat.completions": {Code: "chat.completions", Method: "POST", Route: "/v1/chat/completions", MaxResults: 1},
	"responses.create": {Code: "responses.create", Method: "POST", Route: "/v1/responses", MaxResults: 1},
	"messages.create":  {Code: "messages.create", Method: "POST", Route: "/v1/messages", MaxResults: 1},
	"images.generate":  {Code: "images.generate", Method: "POST", Route: "/v1/images/generations", MaxResults: 10},
	"images.edit":      {Code: "images.edit", Method: "POST", Route: "/v1/images/edits", MaxResults: 10},
	"videos.generate":  {Code: "videos.generate", Method: "POST", Route: "/v1/videos/generations", MaxResults: 1},
}

func newCatalogPlan() *catalogPlan {
	return &catalogPlan{
		channelByKey: make(map[string]*catalogPlanChannel), credentialByKey: make(map[string]*catalogPlanCredential),
		modelByKey: make(map[string]*catalogPlanModel), skuByKey: make(map[string]*catalogPlanSKU),
		productByKey: make(map[string]*catalogPlanProduct), modelSourceType: make(map[string]string),
		rowHandled: make(map[string]bool), issueKeys: make(map[string]bool), mappingKeys: make(map[string]bool),
		apiNameOwners: make(map[string]string), secretOwners: make(map[string]string),
	}
}

func buildCatalogPlan(snapshot catalogSnapshot) *catalogPlan {
	plan := newCatalogPlan()
	plan.buildGatewayChannels(snapshot)
	plan.buildLegacyChannels(snapshot)
	plan.buildVideoChannels(snapshot)
	plan.buildGatewayCredentials(snapshot)
	plan.buildLegacyCredentials(snapshot)
	plan.buildVideoCredentials(snapshot)
	plan.buildDeclaredModels(snapshot)
	plan.buildGatewayAbilities(snapshot)
	plan.buildAccountModels(snapshot)
	plan.buildEndpoints(snapshot)
	plan.buildVideoProducts(snapshot)
	plan.mapEndpointAdapters(snapshot)
	plan.ensureEverySourceRowHandled(snapshot)
	return plan
}

func (p *catalogPlan) issue(row runtimeSourceRow, code, detail string) {
	if strings.TrimSpace(code) == "" {
		code = "invalid_catalog_source"
	}
	key := catalogRowKey(row) + "\x00" + code + "\x00" + detail
	if p.issueKeys[key] {
		return
	}
	p.issueKeys[key] = true
	p.rowHandled[catalogRowKey(row)] = true
	p.issues = append(p.issues, catalogIssue{Row: row, Code: code, Detail: detail})
}

func (p *catalogPlan) mapTarget(row runtimeSourceRow, targetType, targetKey, discriminator string) {
	if row.Table == "" || row.PrimaryKey == "" || targetType == "" || targetKey == "" {
		p.issue(row, "invalid_mapping_plan", "source or target identity is empty")
		return
	}
	key := catalogRowKey(row) + "\x00" + targetType + "\x00" + discriminator
	if p.mappingKeys[key] {
		return
	}
	p.mappingKeys[key] = true
	p.rowHandled[catalogRowKey(row)] = true
	p.mappings = append(p.mappings, catalogPlanMapping{Row: row, TargetType: targetType, TargetKey: targetKey, TargetDiscriminator: discriminator})
}

func (p *catalogPlan) ensureEverySourceRowHandled(snapshot catalogSnapshot) {
	for _, spec := range catalogSourceSpecs {
		for _, row := range snapshot.Tables[spec.Name].Rows {
			if !p.rowHandled[catalogRowKey(row)] {
				p.issue(row, "source_row_unmapped", "the source row has no lossless target mapping")
			}
		}
	}
}

func catalogRowKey(row runtimeSourceRow) string { return row.Table + "\x00" + row.PrimaryKey }

func (p *catalogPlan) requireColumns(table runtimeSourceTable, names ...string) bool {
	if !table.Present {
		return false
	}
	missing := table.missingColumns(names...)
	if len(missing) == 0 {
		return true
	}
	p.issue(runtimeSourceRow{Table: table.Spec.Name, PrimaryKey: "*"}, "source_schema_incomplete", "missing required columns: "+strings.Join(missing, ","))
	return false
}

func (p *catalogPlan) buildGatewayChannels(snapshot catalogSnapshot) {
	table := snapshot.Tables["gw_channels"]
	if !p.requireColumns(table, "id", "name", "protocol", "base_url", "status", "deleted_at") {
		return
	}
	for _, row := range table.Rows {
		p.addChannel(row, "legacy-gw-channel-"+row.PrimaryKey, row.text("name"), row.text("base_url"), catalogSourceStatus(row))
		if catalogJSONHasValues(row.value("extra_headers")) {
			p.issue(row, "unsupported_transport_headers", "legacy channel headers have no executable unified transport representation")
		}
		if catalogJSONHasValues(row.value("config")) {
			p.issue(row, "unsupported_channel_config", "legacy channel behavior must be reviewed before catalog import")
		}
	}
}

func (p *catalogPlan) buildLegacyChannels(snapshot catalogSnapshot) {
	table := snapshot.Tables["channels"]
	if !p.requireColumns(table, "id", "type", "name", "base_url", "callback_secret", "config", "status", "deleted_at") {
		return
	}
	for _, row := range table.Rows {
		p.addChannel(row, "legacy-channel-"+row.PrimaryKey, row.text("name"), row.text("base_url"), catalogSourceStatus(row))
		if row.text("callback_secret") != "" {
			p.issue(row, "callback_secret_requires_review", "legacy callback verification secrets are not execution credentials")
		}
	}
}

func (p *catalogPlan) buildVideoChannels(snapshot catalogSnapshot) {
	table := snapshot.Tables["video_channels"]
	if !p.requireColumns(table, "id", "name", "adapter_type", "base_url", "status", "models", "extra_config") {
		return
	}
	for _, row := range table.Rows {
		status := "disabled"
		if strings.EqualFold(row.text("status"), "active") {
			status = "active"
		}
		p.addChannel(row, "legacy-video-channel-"+row.PrimaryKey, row.text("name"), row.text("base_url"), status)
	}
}

func (p *catalogPlan) addChannel(row runtimeSourceRow, code, name, baseURL, status string) {
	key := catalogRowKey(row)
	if _, exists := p.channelByKey[key]; exists {
		p.issue(row, "duplicate_channel_identity", "source channel identity is duplicated")
		return
	}
	code = catalogGeneratedCode(code)
	name = strings.TrimSpace(name)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if !catalogValidCode(code) || !catalogValidDisplayName(name) {
		p.issue(row, "invalid_channel_identity", "channel code or display name cannot be represented")
		return
	}
	if _, err := catalogBaseHost(baseURL); err != nil {
		p.issue(row, "invalid_channel_origin", "channel base URL is not an allowed HTTP origin")
		return
	}
	if status != "active" {
		status = "disabled"
	}
	channel := &catalogPlanChannel{Key: key, Code: code, Name: name, BaseURL: baseURL, Status: status, Row: row}
	p.channelByKey[key] = channel
	p.channels = append(p.channels, channel)
	p.mapTarget(row, "gateway_channel", key, code)
}

func (p *catalogPlan) buildGatewayCredentials(snapshot catalogSnapshot) {
	table := snapshot.Tables["gw_channel_keys"]
	if !p.requireColumns(table, "id", "channel_id", "name", "api_key", "weight", "status", "max_conc", "deleted_at") {
		return
	}
	for _, row := range table.Rows {
		channelKey := "gw_channels\x00" + row.text("channel_id")
		limit := catalogPositiveLimit(row.text("max_conc"), row, p)
		p.addCredential(row, channelKey, row.text("name"), row.value("api_key"), row.text("weight"), limit, nil, nil)
	}
}

func (p *catalogPlan) buildLegacyCredentials(snapshot catalogSnapshot) {
	table := snapshot.Tables["channel_accounts"]
	if !p.requireColumns(table, "id", "channel_id", "name", "api_key", "config", "weight", "status", "max_tasks", "deleted_at") {
		return
	}
	for _, row := range table.Rows {
		channelKey := "channels\x00" + row.text("channel_id")
		limit := catalogPositiveLimit(row.text("max_tasks"), row, p)
		config, ok := catalogSafeJSON(row, "config", p)
		if !ok {
			continue
		}
		p.addCredential(row, channelKey, row.text("name"), row.value("api_key"), row.text("weight"), nil, limit, config)
	}
}

func (p *catalogPlan) buildVideoCredentials(snapshot catalogSnapshot) {
	table := snapshot.Tables["video_channel_keys"]
	if !p.requireColumns(table, "id", "channel_id", "api_key", "label", "weight", "max_concurrency", "status") {
		return
	}
	for _, row := range table.Rows {
		channelKey := "video_channels\x00" + row.text("channel_id")
		limit := catalogPositiveLimit(row.text("max_concurrency"), row, p)
		p.addCredential(row, channelKey, row.text("label"), row.value("api_key"), row.text("weight"), nil, limit, nil)
	}
}

func (p *catalogPlan) addCredential(row runtimeSourceRow, channelKey, name string, secret runtimeValue, rawWeight string, requestLimit, taskLimit *uint64, config json.RawMessage) {
	channel := p.channelByKey[channelKey]
	if channel == nil {
		p.issue(row, "credential_channel_missing", "credential references a channel that cannot be imported")
		return
	}
	if !secret.Valid || len(secret.Bytes) == 0 || len(secret.Bytes) > 8192 || !catalogPrintableSecret(secret.Bytes) {
		p.issue(row, "invalid_credential_secret", "credential secret is empty or cannot be represented")
		return
	}
	// Scope dedup to (channel, secret) to match the target schema's uniqueness
	// (gw_credential_secret_identities(channel_id, hmac_key_version, secret_hmac)).
	// Legitimate cross-channel reuse of the same api_key must import.
	ownerKey := channelKey + "\x00" + string(secret.Bytes)
	if owner := p.secretOwners[ownerKey]; owner != "" && owner != catalogRowKey(row) {
		p.issue(row, "duplicate_credential_secret", "independent legacy credentials reuse the same secret within one channel")
		return
	}
	p.secretOwners[ownerKey] = catalogRowKey(row)
	weight := uint64(1)
	if rawWeight != "" {
		parsed, err := strconv.ParseInt(rawWeight, 10, 64)
		if err != nil || parsed < 0 || parsed > 1000000 {
			p.issue(row, "invalid_credential_weight", "credential weight is outside the unified range")
			return
		}
		if parsed > 0 {
			weight = uint64(parsed)
		}
	}
	status := catalogSourceStatus(row)
	if channel.Status != "active" {
		status = "disabled"
	}
	key := catalogRowKey(row)
	name = strings.TrimSpace(name)
	if name == "" {
		name = row.Table + " " + row.PrimaryKey
	}
	if !catalogValidDisplayName(name) {
		p.issue(row, "invalid_credential_name", "credential display name cannot be represented")
		return
	}
	base := strings.ReplaceAll(strings.TrimSuffix(row.Table, "s"), "_", "-") + "-" + row.PrimaryKey
	credential := &catalogPlanCredential{
		Key: key, ChannelKey: channelKey, PoolCode: catalogGeneratedCode("legacy-pool-" + base),
		Code: catalogGeneratedCode("legacy-credential-" + base), Name: name, Status: status,
		Secret: append([]byte(nil), secret.Bytes...), Weight: weight, RequestLimit: requestLimit, TaskLimit: taskLimit,
		Row: row, Config: append(json.RawMessage(nil), config...),
	}
	p.credentialByKey[key] = credential
	p.credentials = append(p.credentials, credential)
	p.mapTarget(row, "credential_pool", key, credential.PoolCode)
	p.mapTarget(row, "credential", key, credential.Code)
}

func (p *catalogPlan) buildDeclaredModels(snapshot catalogSnapshot) {
	table := snapshot.Tables["models"]
	if p.requireColumns(table, "code", "name", "type", "protocol", "description", "features", "aliases", "sort", "status", "deleted_at") {
		for _, row := range table.Rows {
			tags, ok := catalogStringList(row, "features", p)
			if !ok {
				continue
			}
			aliases, ok := catalogStringList(row, "aliases", p)
			if !ok {
				continue
			}
			model := p.ensureModel(row, row.text("code"), row.text("name"), row.text("description"), tags, aliases, catalogIntDefault(row.text("sort"), 0), 30)
			if model != nil {
				p.modelSourceType[model.Key] = strings.ToLower(row.text("type"))
				p.mapTarget(row, "model", model.Key, model.Code)
			}
		}
	}
	meta := snapshot.Tables["gw_model_meta"]
	if p.requireColumns(meta, "model_name", "display_name", "features", "sort", "status") {
		for _, row := range meta.Rows {
			tags, ok := catalogStringList(row, "features", p)
			if !ok {
				continue
			}
			model := p.ensureModel(row, row.text("model_name"), row.text("display_name"), "", tags, nil, catalogIntDefault(row.text("sort"), 0), 20)
			if model != nil {
				p.mapTarget(row, "model", model.Key, model.Code)
			}
		}
	}
}

func (p *catalogPlan) ensureModel(row runtimeSourceRow, code, displayName, description string, tags, aliases []string, sortOrder, priority int) *catalogPlanModel {
	code = strings.TrimSpace(code)
	if !catalogValidCode(code) {
		p.issue(row, "invalid_model_identity", "public model identity cannot be represented without changing it")
		return nil
	}
	key := "model\x00" + code
	model := p.modelByKey[key]
	if model == nil {
		model = &catalogPlanModel{Key: key, Code: code, DisplayName: code, Visibility: "visible"}
		p.modelByKey[key] = model
		p.models = append(p.models, model)
	}
	if displayName = strings.TrimSpace(displayName); displayName != "" && priority >= model.MetadataPriority {
		if !catalogValidDisplayName(displayName) || !utf8.ValidString(description) || utf8.RuneCountInString(description) > 1000 {
			p.issue(row, "invalid_model_metadata", "model display metadata cannot be represented")
			return nil
		}
		model.DisplayName, model.Description, model.Sort, model.MetadataPriority = displayName, strings.TrimSpace(description), sortOrder, priority
	}
	active := catalogSourceStatus(row) == "active"
	if !active && model.MetadataPriority <= priority {
		model.Visibility = "hidden"
	} else if active {
		model.Visibility = "visible"
	}
	for _, name := range append([]string{code}, aliases...) {
		name = strings.TrimSpace(name)
		if !catalogValidCode(name) {
			p.issue(row, "invalid_model_alias", "model alias cannot be represented without changing it")
			return nil
		}
		if owner := p.apiNameOwners[name]; owner != "" && owner != key {
			p.issue(row, "model_alias_conflict", "one public API name resolves to more than one model")
			return nil
		}
		p.apiNameOwners[name] = key
		model.Names = catalogAppendUnique(model.Names, name)
	}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" && len(tag) <= 64 {
			model.Tags = catalogAppendUnique(model.Tags, tag)
		}
	}
	sort.Strings(model.Names)
	sort.Strings(model.Tags)
	return model
}

func catalogSourceStatus(row runtimeSourceRow) string {
	if deleted := row.value("deleted_at"); deleted.Valid && strings.TrimSpace(deleted.String()) != "" {
		return "disabled"
	}
	status := strings.ToLower(row.text("status"))
	if status == "1" || status == "active" || status == "enabled" {
		return "active"
	}
	return "disabled"
}

func catalogPositiveLimit(value string, row runtimeSourceRow, p *catalogPlan) *uint64 {
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) == "0" {
		return nil
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 || parsed > 1000000 {
		p.issue(row, "invalid_credential_limit", "credential concurrency limit is outside the unified range")
		return nil
	}
	return &parsed
}

func catalogIntDefault(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func catalogStringList(row runtimeSourceRow, field string, p *catalogPlan) ([]string, bool) {
	value := row.value(field)
	if !value.Valid || len(strings.TrimSpace(value.String())) == 0 || strings.TrimSpace(value.String()) == "null" {
		return nil, true
	}
	var values []string
	decoder := json.NewDecoder(strings.NewReader(value.String()))
	if err := decoder.Decode(&values); err != nil {
		p.issue(row, "invalid_"+field, field+" must be a JSON string array")
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		p.issue(row, "invalid_"+field, field+" must be a JSON string array")
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item != "" {
			result = catalogAppendUnique(result, item)
		}
	}
	return result, true
}

func catalogSafeJSON(row runtimeSourceRow, field string, p *catalogPlan) (json.RawMessage, bool) {
	value := row.value(field)
	if !value.Valid || strings.TrimSpace(value.String()) == "" || strings.TrimSpace(value.String()) == "null" {
		return json.RawMessage(`{}`), true
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(value.String()))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&decoded)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	if decodeErr != nil || trailingErr != io.EOF || catalogContainsSecretField(decoded) {
		p.issue(row, "unsafe_"+field, field+" is invalid or contains secret-bearing or commercial fields")
		return nil, false
	}
	encoded, err := json.Marshal(decoded)
	if err != nil || len(encoded) > 16384 {
		p.issue(row, "invalid_"+field, field+" exceeds the unified catalog limit")
		return nil, false
	}
	return encoded, true
}

func catalogExecutableJSON(row runtimeSourceRow, field string, p *catalogPlan) (json.RawMessage, bool) {
	value := row.value(field)
	if !value.Valid || strings.TrimSpace(value.String()) == "" || strings.TrimSpace(value.String()) == "null" {
		return json.RawMessage(`{}`), true
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(value.String()))
	decoder.UseNumber()
	decodeErr := decoder.Decode(&decoded)
	var trailing any
	trailingErr := decoder.Decode(&trailing)
	if decodeErr != nil || trailingErr != io.EOF || catalogContainsCredentialField(decoded) {
		p.issue(row, "unsafe_"+field, field+" is invalid or contains credential material")
		return nil, false
	}
	catalogStripCommercialFields(decoded)
	encoded, err := json.Marshal(decoded)
	if err != nil || len(encoded) > 16384 {
		p.issue(row, "invalid_"+field, field+" exceeds the unified catalog limit")
		return nil, false
	}
	return encoded, true
}

func catalogContainsCredentialField(value any) bool {
	forbidden := map[string]bool{
		"secret": true, "api_key": true, "apikey": true, "token": true, "authorization": true,
		"callback_secret": true, "password": true,
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbidden[strings.ToLower(strings.TrimSpace(key))] || catalogContainsCredentialField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if catalogContainsCredentialField(child) {
				return true
			}
		}
	}
	return false
}

func catalogStripCommercialFields(value any) {
	commercial := map[string]bool{
		"fixed_price": true, "markup_ratio": true, "surcharge_percent": true,
		"estimated_cost": true, "unit_cost": true, "currency": true,
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if commercial[strings.ToLower(strings.TrimSpace(key))] {
				delete(typed, key)
				continue
			}
			catalogStripCommercialFields(child)
		}
	case []any:
		for _, child := range typed {
			catalogStripCommercialFields(child)
		}
	}
}

func catalogContainsSecretField(value any) bool {
	forbidden := map[string]bool{
		"secret": true, "api_key": true, "apikey": true, "token": true, "authorization": true,
		"callback_secret": true, "password": true, "fixed_price": true, "markup_ratio": true,
		"surcharge_percent": true, "estimated_cost": true, "unit_cost": true, "currency": true,
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if forbidden[strings.ToLower(strings.TrimSpace(key))] || catalogContainsSecretField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if catalogContainsSecretField(child) {
				return true
			}
		}
	}
	return false
}

func catalogJSONHasValues(value runtimeValue) bool {
	if !value.Valid {
		return false
	}
	trimmed := strings.TrimSpace(value.String())
	return trimmed != "" && trimmed != "null" && trimmed != "{}" && trimmed != "[]"
}

func catalogPrintableSecret(secret []byte) bool {
	for _, value := range secret {
		if value < 33 || value > 126 {
			return false
		}
	}
	return true
}

func catalogAppendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func catalogGeneratedCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || strings.ContainsRune("._:/-", character) {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('-')
		}
	}
	value = strings.Trim(builder.String(), "-._:/")
	if len(value) > 112 {
		digest := sha256.Sum256([]byte(value))
		value = strings.TrimRight(value[:95], "-._:/") + "-" + hex.EncodeToString(digest[:8])
	}
	return value
}

func catalogValidCode(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index, character := range value {
		if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || index > 0 && strings.ContainsRune("._:/-", character) {
			continue
		}
		return false
	}
	return true
}

func catalogValidDisplayName(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 128 && !strings.ContainsAny(value, "\x00\r\n\t")
}

func catalogBaseHost(raw string) (catalogAllowedHost, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return catalogAllowedHost{}, fmt.Errorf("invalid origin")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if !catalogValidRemoteHost(host) {
		return catalogAllowedHost{}, fmt.Errorf("invalid host")
	}
	port := uint64(443)
	if parsed.Scheme == "http" {
		port = 80
	}
	if parsed.Port() != "" {
		port, err = strconv.ParseUint(parsed.Port(), 10, 16)
		if err != nil || port == 0 {
			return catalogAllowedHost{}, fmt.Errorf("invalid port")
		}
	}
	return catalogAllowedHost{Protocol: parsed.Scheme, Host: host, Port: uint16(port)}, nil
}

func catalogValidRemoteHost(host string) bool {
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || len(host) > 253 || strings.ContainsAny(host, " /?#@\x00\r\n\t") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
	}
	return true
}

func (p *catalogPlan) buildGatewayAbilities(snapshot catalogSnapshot) {
	table := snapshot.Tables["gw_abilities"]
	if !p.requireColumns(table, "id", "model_name", "channel_id", "key_id", "vendor_model", "priority", "capabilities", "status") {
		return
	}
	transportTable := snapshot.Tables["gw_ability_transports"]
	if transportTable.Present {
		p.requireColumns(transportTable, "id", "ability_id", "transport", "status", "config")
	}
	byAbility := make(map[string][]runtimeSourceRow)
	for _, transportRow := range transportTable.Rows {
		byAbility[transportRow.text("ability_id")] = append(byAbility[transportRow.text("ability_id")], transportRow)
	}
	for _, row := range table.Rows {
		tags, ok := catalogCapabilityTags(row.value("capabilities"))
		if !ok {
			p.issue(row, "invalid_capabilities", "capabilities must be a JSON object with boolean values")
			continue
		}
		model := p.ensureModel(row, row.text("model_name"), row.text("model_name"), "", tags, nil, 0, 10)
		if model == nil {
			continue
		}
		channelKey := "gw_channels\x00" + row.text("channel_id")
		credentialKey := "gw_channel_keys\x00" + row.text("key_id")
		channel, credential := p.channelByKey[channelKey], p.credentialByKey[credentialKey]
		if channel == nil || credential == nil || credential.ChannelKey != channelKey {
			p.issue(row, "ability_route_missing", "ability does not resolve to one channel credential")
			continue
		}
		transports := byAbility[row.PrimaryKey]
		if len(transports) == 0 {
			inferred := runtimeSourceRow{Table: row.Table, PrimaryKey: row.PrimaryKey + ":inferred", Revision: row.Revision, Values: map[string]runtimeValue{
				"transport": {Bytes: []byte(catalogDefaultTransport(snapshot.Tables["gw_channels"], row.text("channel_id"))), Valid: true},
				"status":    row.value("status"),
			}}
			transports = []runtimeSourceRow{inferred}
		}
		var firstProduct *catalogPlanProduct
		for _, transportRow := range transports {
			descriptor, operation, upstreamPath, ok := catalogChatDescriptor(transportRow.text("transport"))
			if !ok {
				p.issue(transportRow, "unsupported_chat_transport", "transport "+transportRow.text("transport")+" is not one of: openai_chat, openai_responses, anthropic_messages, google_generate_content, volcengine_responses_v3")
				continue
			}
			if catalogJSONHasValues(transportRow.value("config")) {
				p.issue(transportRow, "unsupported_transport_config", "chat transport configuration has no executable unified representation")
				continue
			}
			productKey := catalogRowKey(row) + "\x00" + transportRow.PrimaryKey
			product := p.addProduct(catalogPlanProduct{
				Key: productKey, ChannelKey: channelKey, CredentialKey: credentialKey, ModelKey: model.Key,
				Code:        catalogGeneratedCode("legacy-gw-ability-" + row.PrimaryKey + "-" + transportRow.text("transport")),
				VendorModel: row.text("vendor_model"), Adapter: descriptor,
				TransportCode: catalogGeneratedCode("legacy-gw-transport-" + row.PrimaryKey + "-" + transportRow.PrimaryKey),
				BaseURL:       channel.BaseURL, Protocol: descriptor.Protocol, Method: "POST", Path: upstreamPath, AuthScheme: "bearer",
				TaskScope: "none", CancelMode: "none", SourceURLPolicy: "fixed", UpstreamScopeKind: "global", UpstreamScopeKey: "none",
				DeliveryMode: "reference", TimeoutMS: 30000, Priority: catalogPriority(row.text("priority"), row, p), Weight: 1,
				Active:      catalogSourceStatus(row) == "active" && catalogSourceStatus(transportRow) == "active",
				Constraints: json.RawMessage(`{}`), SKUKeys: []string{p.ensureSKU(model, operation, "reference", []string{"standard"}, row)}, Row: row,
			})
			if product == nil {
				continue
			}
			if firstProduct == nil {
				firstProduct = product
			}
			if transportRow.Table == "gw_ability_transports" {
				p.mapTarget(transportRow, "channel_transport", product.Key, product.TransportCode)
			}
		}
		if firstProduct != nil {
			p.mapTarget(row, "product", firstProduct.Key, firstProduct.Code)
		}
	}
	for _, row := range transportTable.Rows {
		if !p.rowHandled[catalogRowKey(row)] {
			p.issue(row, "ability_transport_orphan", "transport does not resolve to an imported ability")
		}
	}
}

func (p *catalogPlan) buildAccountModels(snapshot catalogSnapshot) {
	table := snapshot.Tables["account_models"]
	if !p.requireColumns(table, "id", "account_id", "model_code", "vendor_model", "priority", "status") {
		return
	}
	for _, row := range table.Rows {
		credentialKey := "channel_accounts\x00" + row.text("account_id")
		credential := p.credentialByKey[credentialKey]
		model := p.modelByKey["model\x00"+row.text("model_code")]
		if credential == nil || model == nil {
			p.issue(row, "account_model_reference_missing", "account model does not resolve to one credential and public model")
			continue
		}
		channel := p.channelByKey[credential.ChannelKey]
		protocol := catalogModelProtocol(snapshot.Tables["models"], row.text("model_code"))
		descriptor, operation, upstreamPath, ok := catalogChatDescriptor(protocol)
		if !ok {
			p.issue(row, "unsupported_chat_protocol", "model protocol is not implemented by the unified gateway")
			continue
		}
		vendorModel := row.text("vendor_model")
		if vendorModel == "" {
			vendorModel = model.Code
		}
		product := p.addProduct(catalogPlanProduct{
			Key: catalogRowKey(row), ChannelKey: credential.ChannelKey, CredentialKey: credentialKey, ModelKey: model.Key,
			Code: catalogGeneratedCode("legacy-account-model-" + row.PrimaryKey), VendorModel: vendorModel, Adapter: descriptor,
			TransportCode: catalogGeneratedCode("legacy-account-model-transport-" + row.PrimaryKey), BaseURL: channel.BaseURL,
			Protocol: descriptor.Protocol, Method: "POST", Path: upstreamPath, AuthScheme: "bearer", TaskScope: "none", CancelMode: "none",
			SourceURLPolicy: "fixed", UpstreamScopeKind: "global", UpstreamScopeKey: "none", DeliveryMode: "reference", TimeoutMS: 30000,
			Priority: catalogPriority(row.text("priority"), row, p), Weight: 1, Active: catalogSourceStatus(row) == "active",
			Constraints: catalogMergeConstraints(credential.Config), SKUKeys: []string{p.ensureSKU(model, operation, "reference", []string{"standard"}, row)}, Row: row,
		})
		if product != nil {
			p.mapTarget(row, "product", product.Key, product.Code)
		}
	}
}

func (p *catalogPlan) buildEndpoints(snapshot catalogSnapshot) {
	table := snapshot.Tables["endpoints"]
	if !p.requireColumns(table, "id", "model_code", "route_operation", "supported_operations", "channel_id", "account_id", "protocol", "request_path", "request_method", "content_type", "auth_location", "auth_key", "auth_value_prefix", "vendor_model", "interaction_mode", "supports_stream", "param_mapping", "param_schema", "response_mapping", "poll_path", "callback_mapping", "extra_headers", "extra_config", "timeout", "priority", "status", "deleted_at") {
		return
	}
	bindings := snapshot.Tables["endpoint_accounts"]
	if bindings.Present {
		p.requireColumns(bindings, "id", "endpoint_id", "account_id", "status", "priority", "weight")
	}
	byEndpoint := make(map[string][]runtimeSourceRow)
	for _, binding := range bindings.Rows {
		byEndpoint[binding.text("endpoint_id")] = append(byEndpoint[binding.text("endpoint_id")], binding)
	}
	for _, row := range table.Rows {
		model := p.modelByKey["model\x00"+row.text("model_code")]
		if model == nil {
			model = p.ensureModel(row, row.text("model_code"), row.text("model_code"), "", nil, nil, 0, 5)
		}
		if model == nil {
			continue
		}
		modelType := p.modelSourceType[model.Key]
		operations, ok := catalogEndpointOperations(row, modelType, p)
		if !ok {
			continue
		}
		active := catalogSourceStatus(row) == "active"
		if modelType != "image" || !catalogOnlyImageOperations(operations) {
			if active {
				p.issue(row, "unsupported_endpoint_adapter", "active endpoint is not an OpenAI-compatible image operation")
			} else {
				p.mapTarget(row, "model", model.Key, model.Code)
			}
			continue
		}
		mode := strings.ToLower(row.text("interaction_mode"))
		if mode == "poll" || mode == "callback" {
			p.issue(row, "unsupported_image_async_mode", "poll and callback image endpoints require a dedicated asynchronous adapter")
			continue
		}
		if method := strings.ToUpper(row.text("request_method")); method != "POST" {
			p.issue(row, "unsupported_image_method", "OpenAI-compatible image endpoints require POST")
			continue
		}
		if strings.ToLower(row.text("auth_location")) != "header" || !strings.EqualFold(row.text("auth_key"), "Authorization") || strings.TrimSpace(row.text("auth_value_prefix")) != "Bearer" && strings.TrimSpace(row.text("auth_value_prefix")) != "Bearer " {
			p.issue(row, "unsupported_image_auth", "image endpoint authentication is not bearer Authorization")
			continue
		}
		if catalogJSONHasValues(row.value("extra_headers")) || catalogJSONHasValues(row.value("response_mapping")) || catalogJSONHasValues(row.value("callback_mapping")) || row.text("poll_path") != "" {
			p.issue(row, "unsupported_image_mapping", "image response, callback, polling, or extra-header behavior cannot be preserved by the fixed adapter")
			continue
		}
		config, configOK := catalogImageConfig(row, p)
		if !configOK {
			continue
		}
		channelKey := "channels\x00" + row.text("channel_id")
		channel := p.channelByKey[channelKey]
		if channel == nil {
			p.issue(row, "endpoint_channel_missing", "image endpoint channel cannot be imported")
			continue
		}
		endpointBindings := byEndpoint[row.PrimaryKey]
		if len(endpointBindings) == 0 && row.text("account_id") != "" && row.text("account_id") != "0" {
			endpointBindings = []runtimeSourceRow{{Table: row.Table, PrimaryKey: row.PrimaryKey + ":account", Revision: row.Revision, Values: map[string]runtimeValue{
				"account_id": row.value("account_id"), "status": row.value("status"), "priority": row.value("priority"),
			}}}
		}
		if len(endpointBindings) == 0 {
			if active {
				p.issue(row, "endpoint_credential_missing", "active image endpoint has no account binding")
			} else {
				p.mapTarget(row, "model", model.Key, model.Code)
			}
			continue
		}
		for _, binding := range endpointBindings {
			credentialKey := "channel_accounts\x00" + binding.text("account_id")
			credential := p.credentialByKey[credentialKey]
			if credential == nil || credential.ChannelKey != channelKey {
				p.issue(binding, "endpoint_account_missing", "endpoint account binding cannot be resolved")
				continue
			}
			descriptor, _ := adapter.DescriptorFor("openai_images", 1)
			for _, operation := range operations {
				skuKey := p.ensureSKU(model, operation, "reference", []string{"standard"}, row)
				if skuKey == "" {
					continue
				}
				operationCode := strings.ReplaceAll(operation, ".", "-")
				productKey := catalogRowKey(row) + "\x00" + binding.PrimaryKey + "\x00" + operation
				product := p.addProduct(catalogPlanProduct{
					Key: productKey, ChannelKey: channelKey, CredentialKey: credentialKey, ModelKey: model.Key,
					Code: catalogGeneratedCode("legacy-endpoint-" + row.PrimaryKey + "-account-" + binding.text("account_id") + "-" + operationCode), VendorModel: row.text("vendor_model"), Adapter: descriptor,
					TransportCode: catalogGeneratedCode("legacy-endpoint-transport-" + row.PrimaryKey + "-" + binding.text("account_id") + "-" + operationCode), BaseURL: channel.BaseURL,
					Protocol: descriptor.Protocol, Method: "POST", Path: row.text("request_path"), AuthScheme: "bearer", TaskScope: "none", CancelMode: "none",
					SourceURLPolicy: "fixed", UpstreamScopeKind: "global", UpstreamScopeKey: "none", DeliveryMode: "reference",
					TimeoutMS: catalogTimeoutMS(row.text("timeout"), 120000, row, p), Priority: catalogPriority(binding.text("priority"), binding, p),
					Weight: catalogWeight(binding.text("weight"), binding, p), Active: active && catalogSourceStatus(binding) == "active", Constraints: config, SKUKeys: []string{skuKey}, Row: row,
				})
				if product == nil {
					continue
				}
				p.mapTarget(row, "product", product.Key, product.Code)
				if binding.Table == "endpoint_accounts" {
					p.mapTarget(binding, "offering", product.Key, product.Code)
				}
			}
		}
	}
	for _, binding := range bindings.Rows {
		if !p.rowHandled[catalogRowKey(binding)] {
			p.issue(binding, "endpoint_account_orphan", "binding does not resolve to an imported endpoint")
		}
	}
}

func (p *catalogPlan) buildVideoProducts(snapshot catalogSnapshot) {
	table := snapshot.Tables["video_channels"]
	if !p.requireColumns(table, "id", "adapter_type", "base_url", "status", "priority", "request_timeout_seconds", "models", "capabilities", "result_storage_enabled", "extra_config") {
		return
	}
	for _, row := range table.Rows {
		channelKey := catalogRowKey(row)
		channel := p.channelByKey[channelKey]
		if channel == nil {
			continue
		}
		mappings, err := video.ParseVideoModelMappings(row.value("models").Bytes)
		if err != nil {
			p.issue(row, "invalid_video_models", err.Error())
			continue
		}
		tags, ok := catalogCapabilityTags(row.value("capabilities"))
		if !ok {
			p.issue(row, "invalid_video_capabilities", "video capabilities must be a JSON object with boolean values")
			continue
		}
		tags = catalogAppendUnique(tags, "video")
		adapterCode := strings.ToLower(row.text("adapter_type"))
		descriptor, ok := adapter.DescriptorFor(adapterCode, 1)
		if !ok || (adapterCode != "seedance" && adapterCode != "generic") {
			p.issue(row, "unsupported_video_adapter", "video adapter is not implemented by the unified runtime")
			continue
		}
		method, path, constraints, tiers, ok := catalogVideoTransport(row, adapterCode, p)
		if !ok {
			continue
		}
		deliveryMode := "reference"
		if catalogBool(row.text("result_storage_enabled")) {
			deliveryMode = "managed_copy"
		}
		timeoutMS := catalogSecondsToTimeoutMS(row.text("request_timeout_seconds"), 30000, row, p)
		priority := catalogPriority(row.text("priority"), row, p)
		for _, mapping := range mappings {
			var validationErr error
			switch adapterCode {
			case "generic":
				validationErr = adapter.ValidateGenericVideoCatalog(channel.BaseURL, method, path, mapping.VendorModel, constraints)
			case "seedance":
				validationErr = adapter.ValidateSeedanceVideoCatalog(method, path)
			}
			if validationErr != nil {
				p.issue(row, "invalid_video_catalog_contract", mapping.ModelName+": "+validationErr.Error())
				continue
			}
			model := p.ensureModel(row, mapping.ModelName, mapping.ModelName, "", tags, nil, 0, 25)
			if model == nil {
				continue
			}
			p.modelSourceType[model.Key] = "video"
			skuKey := p.ensureSKU(model, "videos.generate", deliveryMode, tiers, row)
			if skuKey == "" {
				continue
			}
			for _, credential := range p.credentials {
				if credential.ChannelKey != channelKey {
					continue
				}
				product := p.addProduct(catalogPlanProduct{
					Key:        catalogRowKey(row) + "\x00" + mapping.ModelName + "\x00" + credential.Key,
					ChannelKey: channelKey, CredentialKey: credential.Key, ModelKey: model.Key,
					Code:        catalogGeneratedCode("legacy-video-" + row.PrimaryKey + "-" + mapping.ModelName + "-" + credential.Row.PrimaryKey),
					VendorModel: mapping.VendorModel, Adapter: descriptor,
					TransportCode: catalogGeneratedCode("legacy-video-transport-" + row.PrimaryKey + "-" + mapping.ModelName + "-" + credential.Row.PrimaryKey),
					BaseURL:       channel.BaseURL, Protocol: descriptor.Protocol, Method: method, Path: path, AuthScheme: "bearer",
					TaskScope: "task", CancelMode: "none", SourceURLPolicy: "fixed",
					UpstreamScopeKind: "credential", UpstreamScopeKey: credential.Code,
					DeliveryMode: deliveryMode, TimeoutMS: timeoutMS, Priority: priority, Weight: 1,
					Active:      channel.Status == "active" && credential.Status == "active",
					Constraints: constraints,
					Actions: []catalogPlanAction{
						{Code: "submit", AllowedSourceState: "allocated", IdempotencyMode: "user_keyed"},
						{Code: "query", AllowedSourceState: "accepted", IdempotencyMode: "none"},
					},
					SKUKeys: []string{skuKey}, Row: row,
				})
				if product != nil {
					p.mapTarget(row, "product", product.Key, product.Code)
				}
			}
		}
	}
}

func catalogVideoTransport(row runtimeSourceRow, adapterCode string, p *catalogPlan) (string, string, json.RawMessage, []string, bool) {
	switch adapterCode {
	case "seedance":
		if catalogJSONHasValues(row.value("extra_config")) {
			p.issue(row, "unsupported_seedance_config", "native Seedance channel configuration is not part of the fixed adapter contract")
			return "", "", nil, nil, false
		}
		return "POST", adapter.SeedanceSubmitPath, json.RawMessage(`{}`), []string{"standard"}, true
	case "generic":
		config, ok := catalogExecutableJSON(row, "extra_config", p)
		if !ok {
			return "", "", nil, nil, false
		}
		var configObject map[string]any
		if err := json.Unmarshal(config, &configObject); err != nil || configObject == nil {
			p.issue(row, "invalid_generic_video_config", "generic video configuration must be a JSON object")
			return "", "", nil, nil, false
		}
		adapterConfig, exists := configObject["adapter"]
		if !exists {
			adapterConfig = map[string]any{}
			configObject["adapter"] = adapterConfig
		}
		adapterObject, ok := adapterConfig.(map[string]any)
		if !ok {
			p.issue(row, "invalid_generic_video_config", "generic video adapter configuration must be a JSON object")
			return "", "", nil, nil, false
		}
		adapterObject["profile"] = row.text("adapter_profile")
		config, err := json.Marshal(configObject)
		if err != nil || len(config) > 16384 {
			p.issue(row, "invalid_generic_video_config", "generic video configuration exceeds the unified catalog limit")
			return "", "", nil, nil, false
		}
		var envelope struct {
			Adapter struct {
				Profile string `json:"profile"`
				Submit  struct {
					Method string `json:"method"`
					Path   string `json:"path"`
				} `json:"submit"`
				ServiceTiers map[string]json.RawMessage `json:"service_tiers"`
			} `json:"adapter"`
		}
		if err := json.Unmarshal(config, &envelope); err != nil || envelope.Adapter.Profile != "json_task_v1" {
			p.issue(row, "invalid_generic_video_config", "generic video configuration must use json_task_v1")
			return "", "", nil, nil, false
		}
		method := strings.ToUpper(strings.TrimSpace(envelope.Adapter.Submit.Method))
		path := strings.TrimSpace(envelope.Adapter.Submit.Path)
		if method == "" {
			method = "POST"
		}
		if path == "" {
			p.issue(row, "invalid_generic_video_config", "generic video submit path is required")
			return "", "", nil, nil, false
		}
		tiers := []string{"standard"}
		for tier := range envelope.Adapter.ServiceTiers {
			tier = strings.ToLower(strings.TrimSpace(tier))
			if !catalogValidCode(tier) {
				p.issue(row, "invalid_video_service_tier", "generic video service tier cannot be represented")
				return "", "", nil, nil, false
			}
			tiers = catalogAppendUnique(tiers, tier)
		}
		sort.Strings(tiers)
		return method, path, config, tiers, true
	default:
		return "", "", nil, nil, false
	}
}

func (p *catalogPlan) mapEndpointAdapters(snapshot catalogSnapshot) {
	adapters := snapshot.Tables["endpoint_adapters"]
	if p.requireColumns(adapters, "id", "endpoint_id", "code", "active_revision_id", "status", "config", "deleted_at") {
		for _, row := range adapters.Rows {
			p.issue(row, "endpoint_adapter_requires_review", "legacy endpoint adapters are mutable data and cannot become a server-owned executable implementation")
		}
	}
	revisions := snapshot.Tables["endpoint_adapter_revisions"]
	if p.requireColumns(revisions, "id", "adapter_id", "version", "digest", "config", "deleted_at") {
		for _, row := range revisions.Rows {
			p.issue(row, "endpoint_adapter_revision_requires_review", "legacy adapter revision must be replaced by a deployed adapter contract")
		}
	}
}

func (p *catalogPlan) addProduct(value catalogPlanProduct) *catalogPlanProduct {
	if value.Key == "" || p.productByKey[value.Key] != nil {
		p.issue(value.Row, "duplicate_product_identity", "catalog product identity is duplicated")
		return nil
	}
	channel, credential := p.channelByKey[value.ChannelKey], p.credentialByKey[value.CredentialKey]
	if channel == nil || credential == nil || credential.ChannelKey != value.ChannelKey || p.modelByKey[value.ModelKey] == nil {
		p.issue(value.Row, "product_reference_missing", "catalog product references an unknown channel, credential, or model")
		return nil
	}
	value.Code = catalogGeneratedCode(value.Code)
	value.TransportCode = catalogGeneratedCode(value.TransportCode)
	value.VendorModel = strings.TrimSpace(value.VendorModel)
	value.Protocol = strings.ToLower(strings.TrimSpace(value.Protocol))
	value.Method = strings.ToUpper(strings.TrimSpace(value.Method))
	value.Path = strings.TrimSpace(value.Path)
	value.AuthScheme = strings.ToLower(strings.TrimSpace(value.AuthScheme))
	if !catalogValidCode(value.Code) || !catalogValidCode(value.TransportCode) || !catalogValidCode(value.VendorModel) ||
		value.Adapter.Code == "" || value.Adapter.Version == 0 || value.Adapter.Protocol != value.Protocol ||
		value.Method == "" || !strings.HasPrefix(value.Path, "/") || strings.HasPrefix(value.Path, "//") || value.AuthScheme != "bearer" ||
		value.TimeoutMS < 100 || value.TimeoutMS > 900000 || value.Weight == 0 || value.Weight > 1000000 || len(value.SKUKeys) == 0 {
		p.issue(value.Row, "invalid_product_contract", "catalog product cannot satisfy the executable transport contract")
		return nil
	}
	for _, existing := range p.products {
		if existing.Code == value.Code || existing.TransportCode == value.TransportCode {
			p.issue(value.Row, "duplicate_product_code", "catalog product or transport code is duplicated")
			return nil
		}
	}
	if _, err := catalogBaseHost(value.BaseURL); err != nil {
		p.issue(value.Row, "invalid_product_origin", "catalog product base URL is invalid")
		return nil
	}
	for _, skuKey := range value.SKUKeys {
		if p.skuByKey[skuKey] == nil {
			p.issue(value.Row, "product_sku_missing", "catalog product references an unknown SKU")
			return nil
		}
	}
	if len(value.Constraints) == 0 {
		value.Constraints = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(value.Constraints, &object); err != nil || catalogContainsSecretField(object) {
		p.issue(value.Row, "invalid_product_constraints", "catalog product constraints are invalid or contain protected fields")
		return nil
	}
	value.CostPlanCode = catalogGeneratedCode("legacy-cost-" + value.Code)
	value.DeliveryMode = strings.ToLower(strings.TrimSpace(value.DeliveryMode))
	if value.DeliveryMode != "reference" && value.DeliveryMode != "managed_copy" {
		p.issue(value.Row, "invalid_delivery_mode", "catalog product delivery mode is invalid")
		return nil
	}
	p.productByKey[value.Key] = &value
	p.products = append(p.products, &value)
	return &value
}

func (p *catalogPlan) ensureSKU(model *catalogPlanModel, operation, deliveryMode string, serviceTiers []string, row runtimeSourceRow) string {
	contract, ok := catalogOperations[operation]
	if model == nil || !ok || (deliveryMode != "reference" && deliveryMode != "managed_copy") {
		p.issue(row, "invalid_sku_contract", "catalog SKU operation or delivery mode is invalid")
		return ""
	}
	tiers := make([]string, 0, len(serviceTiers)+1)
	for _, tier := range serviceTiers {
		tier = strings.ToLower(strings.TrimSpace(tier))
		if !catalogValidCode(tier) {
			p.issue(row, "invalid_sku_service_tier", "catalog SKU service tier cannot be represented")
			return ""
		}
		tiers = catalogAppendUnique(tiers, tier)
	}
	if len(tiers) == 0 {
		tiers = []string{"standard"}
	}
	sort.Strings(tiers)
	key := model.Key + "\x00" + operation + "\x00" + deliveryMode + "\x00" + strings.Join(tiers, ",")
	if p.skuByKey[key] != nil {
		return key
	}
	idempotency := "optional"
	sku := &catalogPlanSKU{
		Key: key, ModelKey: model.Key, Operation: operation, Method: contract.Method, Route: contract.Route,
		Code:         catalogGeneratedCode("legacy-" + model.Code + "-" + strings.ReplaceAll(operation, ".", "-") + "-" + deliveryMode),
		DeliveryMode: deliveryMode, IdempotencyMode: idempotency, MaxResults: contract.MaxResults, ServiceTiers: tiers,
	}
	for _, existing := range p.skus {
		if existing.Code == sku.Code {
			p.issue(row, "duplicate_sku_code", "catalog SKU code is duplicated")
			return ""
		}
	}
	p.skuByKey[key] = sku
	p.skus = append(p.skus, sku)
	return key
}

func catalogCapabilityTags(value runtimeValue) ([]string, bool) {
	if !value.Valid || strings.TrimSpace(value.String()) == "" || strings.TrimSpace(value.String()) == "null" {
		return nil, true
	}
	var capabilities map[string]bool
	decoder := json.NewDecoder(strings.NewReader(value.String()))
	if err := decoder.Decode(&capabilities); err != nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	tags := make([]string, 0, len(capabilities))
	for name, enabled := range capabilities {
		name = strings.ToLower(strings.TrimSpace(name))
		if enabled && catalogValidCode(name) {
			tags = append(tags, name)
		} else if enabled {
			return nil, false
		}
	}
	sort.Strings(tags)
	return tags, true
}

func catalogDefaultTransport(table runtimeSourceTable, channelID string) string {
	for _, row := range table.Rows {
		if row.PrimaryKey != channelID {
			continue
		}
		switch strings.ToLower(row.text("protocol")) {
		case "responses", "openai_responses":
			return "openai_responses"
		case "anthropic", "anthropic_messages":
			return "anthropic_messages"
		case "google", "google_generate_content":
			return "google_generate_content"
		case "volcengine", "volcengine_responses_v3":
			return "volcengine_responses_v3"
		default:
			// Fall back to OpenAI Chat for unknown or custom provider
			// protocols (grok / mirainya / duomi and similar OpenAI-compatible
			// proxies) so a bespoke protocol string does not sink the whole
			// legacy catalog import. Explicit gw_ability_transports rows still
			// take precedence when set.
			return "openai_chat"
		}
	}
	return ""
}

func catalogChatDescriptor(raw string) (adapter.Descriptor, string, string, bool) {
	var code, operation, path string
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "openai", "openai_chat", "chat_completions":
		code, operation, path = "openai_chat", "chat.completions", "/v1/chat/completions"
	case "responses", "openai_responses":
		code, operation, path = "openai_responses", "responses.create", "/v1/responses"
	case "anthropic", "anthropic_messages":
		code, operation, path = "anthropic_messages", "messages.create", "/v1/messages"
	case "google", "google_generate_content":
		code, operation, path = "google_generate_content", "responses.create", "/v1beta/models/generateContent"
	case "volcengine", "volcengine_responses_v3":
		code, operation, path = "volcengine_responses_v3", "responses.create", "/api/v3/responses"
	default:
		return adapter.Descriptor{}, "", "", false
	}
	descriptor, ok := adapter.DescriptorFor(code, 1)
	return descriptor, operation, path, ok
}

func catalogPriority(value string, row runtimeSourceRow, p *catalogPlan) uint32 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > int64(^uint32(0)) {
		p.issue(row, "invalid_route_priority", "legacy route priority is outside the unified range")
		return 0
	}
	return uint32(parsed)
}

func catalogWeight(value string, row runtimeSourceRow, p *catalogPlan) uint64 {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return 1
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 || parsed > 1000000 {
		p.issue(row, "invalid_route_weight", "legacy route weight is outside the unified range")
		return 1
	}
	return parsed
}

func catalogTimeoutMS(value string, fallback uint64, row runtimeSourceRow, p *catalogPlan) uint64 {
	value = strings.TrimSpace(value)
	if value == "" || value == "0" {
		return fallback
	}
	seconds, err := strconv.ParseUint(value, 10, 64)
	if err != nil || seconds == 0 || seconds > 900 || seconds > ^uint64(0)/1000 {
		p.issue(row, "invalid_transport_timeout", "legacy timeout is outside the unified range")
		return fallback
	}
	return seconds * 1000
}

func catalogSecondsToTimeoutMS(value string, fallback uint64, row runtimeSourceRow, p *catalogPlan) uint64 {
	return catalogTimeoutMS(value, fallback, row, p)
}

func catalogMergeConstraints(values ...json.RawMessage) json.RawMessage {
	merged := make(map[string]any)
	for _, raw := range values {
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var object map[string]any
		if json.Unmarshal(raw, &object) != nil {
			continue
		}
		for key, value := range object {
			merged[key] = value
		}
	}
	encoded, err := json.Marshal(merged)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func catalogModelProtocol(table runtimeSourceTable, modelCode string) string {
	for _, row := range table.Rows {
		if row.PrimaryKey == modelCode || row.text("code") == modelCode {
			protocol := strings.ToLower(row.text("protocol"))
			if protocol == "" {
				return "openai"
			}
			return protocol
		}
	}
	return "openai"
}

func catalogEndpointOperations(row runtimeSourceRow, modelType string, p *catalogPlan) ([]string, bool) {
	operations := make([]string, 0, 2)
	if catalogJSONHasValues(row.value("supported_operations")) {
		var declared []string
		decoder := json.NewDecoder(strings.NewReader(row.value("supported_operations").String()))
		if err := decoder.Decode(&declared); err != nil {
			p.issue(row, "invalid_endpoint_operations", "supported_operations must be a JSON string array")
			return nil, false
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			p.issue(row, "invalid_endpoint_operations", "supported_operations must contain one JSON value")
			return nil, false
		}
		for _, operation := range declared {
			operation = strings.TrimSpace(operation)
			if operation != "" {
				operations = catalogAppendUnique(operations, operation)
			}
		}
	}
	if len(operations) == 0 && row.text("route_operation") != "" {
		operations = append(operations, row.text("route_operation"))
	}
	if len(operations) == 0 {
		switch strings.ToLower(modelType) {
		case "image":
			if strings.Contains(strings.ToLower(row.text("request_path")), "/images/edits") {
				operations = []string{"images.edit"}
			} else {
				operations = []string{"images.generate"}
			}
		case "video":
			operations = []string{"videos.generate"}
		}
	}
	for _, operation := range operations {
		if _, exists := catalogOperations[operation]; !exists {
			p.issue(row, "unsupported_endpoint_operation", "endpoint operation is not implemented by the unified gateway")
			return nil, false
		}
	}
	sort.Strings(operations)
	return operations, len(operations) != 0
}

func catalogOnlyImageOperations(operations []string) bool {
	if len(operations) == 0 {
		return false
	}
	for _, operation := range operations {
		if operation != "images.generate" && operation != "images.edit" {
			return false
		}
	}
	return true
}

func catalogImageConfig(row runtimeSourceRow, p *catalogPlan) (json.RawMessage, bool) {
	for _, field := range []string{"param_mapping", "param_schema", "extra_config"} {
		if catalogJSONHasValues(row.value(field)) {
			p.issue(row, "unsupported_image_"+field, "fixed OpenAI image adapter cannot preserve legacy "+field)
			return nil, false
		}
	}
	if contentType := strings.ToLower(row.text("content_type")); contentType != "" && contentType != "application/json" && !strings.HasPrefix(contentType, "multipart/form-data") {
		p.issue(row, "unsupported_image_content_type", "image endpoint content type is not supported")
		return nil, false
	}
	return json.RawMessage(`{}`), true
}

func catalogBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
