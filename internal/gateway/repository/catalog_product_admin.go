package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type CatalogAdapterInput struct {
	Code                   string `json:"-"`
	Version                uint32 `json:"-"`
	Protocol               string `json:"-"`
	ImplementationDigest   string `json:"-"`
	MinimumSemanticVersion string `json:"-"`
}

type CatalogAllowedHostInput struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     uint16 `json:"port"`
}

type CatalogProductActionInput struct {
	ActionCode            string `json:"action_code"`
	AllowedSourceState    string `json:"allowed_source_state"`
	IdempotencyMode       string `json:"idempotency_mode"`
	RequestSchemaVersion  uint32 `json:"request_schema_version"`
	ResponseSchemaVersion uint32 `json:"response_schema_version"`
}

type CatalogRouteInput struct {
	SKUID    uint64 `json:"sku_id"`
	Priority uint32 `json:"priority"`
	Weight   uint64 `json:"weight"`
}

type CatalogProductInput struct {
	ExpectedVersion               uint64                      `json:"expected_version"`
	ChannelID                     uint64                      `json:"channel_id"`
	CredentialPoolID              uint64                      `json:"credential_pool_id"`
	ProductCode                   string                      `json:"product_code"`
	VendorModel                   string                      `json:"vendor_model"`
	CapabilityConstraints         json.RawMessage             `json:"capability_constraints"`
	ConstraintsSchemaVersion      uint32                      `json:"constraints_schema_version"`
	AdapterCode                   string                      `json:"adapter_code"`
	AdapterVersion                uint32                      `json:"adapter_version"`
	TransportCode                 string                      `json:"transport_code"`
	BaseURL                       string                      `json:"base_url"`
	Protocol                      string                      `json:"protocol"`
	RequestMethod                 string                      `json:"request_method"`
	RequestPath                   string                      `json:"request_path"`
	AuthScheme                    string                      `json:"auth_scheme"`
	TransportTimeoutMS            uint64                      `json:"transport_timeout_ms"`
	TaskTimeoutMS                 uint64                      `json:"task_timeout_ms"`
	TaskScope                     string                      `json:"task_scope"`
	CancelMode                    string                      `json:"cancel_mode"`
	SourceURLPolicy               string                      `json:"source_url_policy"`
	UpstreamScopeKind             string                      `json:"upstream_scope_kind"`
	UpstreamScopeKey              string                      `json:"upstream_scope_key"`
	StateCompatibilityFingerprint string                      `json:"state_compatibility_fingerprint,omitempty"`
	AllowedHosts                  []CatalogAllowedHostInput   `json:"allowed_hosts"`
	Actions                       []CatalogProductActionInput `json:"actions"`
	CostPlanCode                  string                      `json:"cost_plan_code"`
	Routes                        []CatalogRouteInput         `json:"routes"`
	Adapter                       CatalogAdapterInput         `json:"-"`
}

func (in *CatalogProductInput) Normalize() error {
	in.ProductCode = strings.ToLower(strings.TrimSpace(in.ProductCode))
	if !validUpstreamVendorModel(in.VendorModel) {
		return ErrInvalidInput
	}
	in.VendorModel = strings.TrimSpace(in.VendorModel)
	in.AdapterCode = strings.ToLower(strings.TrimSpace(in.AdapterCode))
	in.TransportCode = strings.ToLower(strings.TrimSpace(in.TransportCode))
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.Protocol = strings.ToLower(strings.TrimSpace(in.Protocol))
	in.RequestMethod = strings.ToUpper(strings.TrimSpace(in.RequestMethod))
	in.RequestPath = strings.TrimSpace(in.RequestPath)
	in.AuthScheme = strings.ToLower(strings.TrimSpace(in.AuthScheme))
	in.TaskScope = strings.ToLower(strings.TrimSpace(in.TaskScope))
	in.CancelMode = strings.ToLower(strings.TrimSpace(in.CancelMode))
	in.SourceURLPolicy = strings.ToLower(strings.TrimSpace(in.SourceURLPolicy))
	in.UpstreamScopeKind = strings.ToLower(strings.TrimSpace(in.UpstreamScopeKind))
	in.UpstreamScopeKey = strings.TrimSpace(in.UpstreamScopeKey)
	in.CostPlanCode = strings.ToLower(strings.TrimSpace(in.CostPlanCode))
	in.StateCompatibilityFingerprint = strings.ToLower(strings.TrimSpace(in.StateCompatibilityFingerprint))
	for index := range in.AllowedHosts {
		in.AllowedHosts[index].Protocol = strings.ToLower(strings.TrimSpace(in.AllowedHosts[index].Protocol))
		in.AllowedHosts[index].Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.AllowedHosts[index].Host), "."))
	}
	for index := range in.Actions {
		in.Actions[index].ActionCode = strings.ToLower(strings.TrimSpace(in.Actions[index].ActionCode))
		in.Actions[index].AllowedSourceState = strings.ToLower(strings.TrimSpace(in.Actions[index].AllowedSourceState))
		in.Actions[index].IdempotencyMode = strings.ToLower(strings.TrimSpace(in.Actions[index].IdempotencyMode))
	}
	constraints, err := normalizeCatalogJSON(in.CapabilityConstraints)
	if err != nil {
		return err
	}
	in.CapabilityConstraints = constraints
	return nil
}

func (in CatalogProductInput) Validate() error {
	if in.ExpectedVersion == 0 || in.ChannelID == 0 || in.CredentialPoolID == 0 ||
		!catalogIdentityPattern.MatchString(in.ProductCode) || !validUpstreamVendorModel(in.VendorModel) ||
		in.ConstraintsSchemaVersion == 0 || in.ConstraintsSchemaVersion > 100 || len(in.CapabilityConstraints) > 16384 ||
		!catalogIdentityPattern.MatchString(in.AdapterCode) || in.AdapterVersion == 0 || in.Adapter.Code != in.AdapterCode || in.Adapter.Version != in.AdapterVersion ||
		in.Adapter.Protocol != in.Protocol || !validHexDigest(in.Adapter.ImplementationDigest, 32) || !semanticVersionPattern.MatchString(in.Adapter.MinimumSemanticVersion) ||
		!catalogIdentityPattern.MatchString(in.TransportCode) || !catalogIdentityPattern.MatchString(in.Protocol) || in.Protocol == "catalog_discovery" ||
		!validCatalogRequestMethod(in.AdapterCode, in.RequestMethod) || !validCatalogRequestTarget(in.RequestPath) ||
		in.AuthScheme != "bearer" || in.TransportTimeoutMS < 100 || in.TransportTimeoutMS > 300000 || in.TaskTimeoutMS < 100 || in.TaskTimeoutMS > 300000 ||
		(in.TaskScope != "none" && in.TaskScope != "request" && in.TaskScope != "task") ||
		(in.CancelMode != "none" && in.CancelMode != "upstream" && in.CancelMode != "local_only") ||
		(in.SourceURLPolicy != "fixed" && in.SourceURLPolicy != "refreshable") || !validScope(in.UpstreamScopeKind) ||
		in.UpstreamScopeKey == "" || len(in.UpstreamScopeKey) > 255 || !catalogIdentityPattern.MatchString(in.CostPlanCode) ||
		len(in.Routes) == 0 || len(in.Routes) > 256 || len(in.AllowedHosts) > 32 || len(in.Actions) > 16 {
		return ErrInvalidInput
	}
	if !validCatalogTaskPolicy(in.AdapterCode, in.TaskScope, in.CancelMode) {
		return ErrInvalidInput
	}
	if in.StateCompatibilityFingerprint != "" && !validHexDigest(in.StateCompatibilityFingerprint, 32) {
		return ErrInvalidInput
	}
	if _, err := catalogAllowedHosts(in); err != nil {
		return err
	}
	seenRoutes := make(map[uint64]struct{}, len(in.Routes))
	for _, route := range in.Routes {
		if route.SKUID == 0 || route.Weight == 0 || route.Weight > 1000000 {
			return ErrInvalidInput
		}
		if _, exists := seenRoutes[route.SKUID]; exists {
			return ErrInvalidInput
		}
		seenRoutes[route.SKUID] = struct{}{}
	}
	seenActions := make(map[string]struct{}, len(in.Actions))
	for _, action := range in.Actions {
		if !validProductAction(action) {
			return ErrInvalidInput
		}
		if action.ActionCode == "cancel" && in.CancelMode != "upstream" {
			return ErrInvalidInput
		}
		if _, exists := seenActions[action.ActionCode]; exists {
			return ErrInvalidInput
		}
		seenActions[action.ActionCode] = struct{}{}
	}
	if in.TaskScope == "task" {
		if _, ok := seenActions["submit"]; !ok {
			return ErrInvalidInput
		}
		if _, ok := seenActions["query"]; !ok {
			return ErrInvalidInput
		}
	}
	if in.CancelMode == "upstream" {
		if _, ok := seenActions["cancel"]; !ok {
			return ErrInvalidInput
		}
	}
	return nil
}

// validUpstreamVendorModel accepts the provider's opaque model identifier
// without weakening Prism-owned codes. The database column is varchar(255),
// and control characters have no valid role in an upstream request value.
func validUpstreamVendorModel(value string) bool {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n\t") {
		return false
	}
	value = strings.TrimSpace(value)
	return value != "" && len(value) <= 255
}

func validCatalogRequestMethod(adapterCode, method string) bool {
	if adapterCode != "generic" {
		return method == "POST"
	}
	switch method {
	case "GET", "POST", "DELETE", "PUT", "PATCH":
		return true
	default:
		return false
	}
}

func validCatalogRequestTarget(value string) bool {
	if value == "" || len(value) > 255 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "#\x00\r\n") {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.Opaque != "" || parsed.Fragment != "" || !strings.HasPrefix(parsed.Path, "/") {
		return false
	}
	_, err = url.QueryUnescape(parsed.RawQuery)
	return err == nil
}

func validCatalogTaskPolicy(adapterCode, taskScope, cancelMode string) bool {
	if cancelMode != "none" {
		return false
	}
	if adapterCode == "generic" || adapterCode == "seedance" {
		return taskScope == "task"
	}
	if adapterCode == "openai_images" {
		return taskScope == "none" || taskScope == "request" || taskScope == "task"
	}
	return taskScope == "none" || taskScope == "request"
}

func normalizeCatalogJSON(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte(`{}`), nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, ErrInvalidInput
	}
	object, ok := value.(map[string]any)
	if !ok || containsForbiddenCatalogField(object) {
		return nil, ErrInvalidInput
	}
	result, err := json.Marshal(object)
	if err != nil || len(result) > 16384 {
		return nil, ErrInvalidInput
	}
	return result, nil
}

func containsForbiddenCatalogField(value any) bool {
	forbidden := map[string]struct{}{
		"fixed_price": {}, "markup_ratio": {}, "surcharge_percent": {}, "estimated_cost": {}, "unit_cost": {}, "currency": {},
		"secret": {}, "api_key": {}, "token": {}, "authorization": {}, "callback_secret": {},
	}
	switch current := value.(type) {
	case map[string]any:
		for name, child := range current {
			if _, blocked := forbidden[strings.ToLower(strings.TrimSpace(name))]; blocked || containsForbiddenCatalogField(child) {
				return true
			}
		}
	case []any:
		for _, child := range current {
			if containsForbiddenCatalogField(child) {
				return true
			}
		}
	}
	return false
}

func validateCatalogBaseURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" && parsed.Scheme != "http" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrInvalidInput
	}
	if !validRemoteHost(parsed.Hostname()) {
		return nil, ErrInvalidInput
	}
	if parsed.Port() != "" {
		port, err := strconv.ParseUint(parsed.Port(), 10, 16)
		if err != nil || port == 0 {
			return nil, ErrInvalidInput
		}
	}
	return parsed, nil
}

func validRemoteHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || !utf8.ValidString(host) || len(host) > 253 {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
	}
	return !strings.ContainsAny(host, " /?#@\x00\r\n\t")
}

func validExactRemoteHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if !validRemoteHost(host) {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.ContainsAny(host, "*[]:") {
		return false
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func validProductAction(action CatalogProductActionInput) bool {
	switch action.ActionCode {
	case "submit", "recover", "query", "cancel", "named_action", "result_fetch":
	default:
		return false
	}
	if action.AllowedSourceState == "" || len(action.AllowedSourceState) > 32 || action.RequestSchemaVersion == 0 || action.ResponseSchemaVersion == 0 {
		return false
	}
	return action.IdempotencyMode == "none" || action.IdempotencyMode == "upstream" || action.IdempotencyMode == "user_keyed"
}

func (s *Store) CreateCatalogProduct(ctx context.Context, tx *sql.Tx, releaseID uint64, in CatalogProductInput, actorID uint64) (uint64, error) {
	if tx == nil || releaseID == 0 || actorID == 0 {
		return 0, ErrInvalidInput
	}
	if err := in.Normalize(); err != nil {
		return 0, err
	}
	if err := in.Validate(); err != nil {
		return 0, err
	}
	lock, err := lockCatalogDraft(ctx, tx, releaseID, in.ExpectedVersion)
	if err != nil {
		return 0, err
	}
	productID, offeringID, err := createCatalogProductRows(ctx, tx, releaseID, in)
	if err != nil {
		return 0, err
	}
	return productID, advanceCatalogDraft(ctx, tx, releaseID, lock, "unified.catalog.product.create", productID, actorID, ginSafeMetadata{"product_code": in.ProductCode, "transport_code": in.TransportCode, "offering_id": offeringID})
}

// CreateActiveCatalogProduct adds a complete upstream product graph directly to
// the active release. The active pointer and config version are both checked
// before any content row is written; no draft or release snapshot is created.
func (s *Store) CreateActiveCatalogProduct(ctx context.Context, tx *sql.Tx, expectedActiveReleaseID, expectedConfigVersion uint64, in CatalogProductInput, actorID uint64) (CatalogChangeResult, error) {
	if tx == nil || expectedActiveReleaseID == 0 || expectedConfigVersion == 0 || actorID == 0 {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	if in.ExpectedVersion != 0 && in.ExpectedVersion != expectedConfigVersion {
		return CatalogChangeResult{}, ErrInvalidInput
	}
	in.ExpectedVersion = expectedConfigVersion
	if err := in.Normalize(); err != nil {
		return CatalogChangeResult{}, err
	}
	if err := in.Validate(); err != nil {
		return CatalogChangeResult{}, err
	}
	active, err := lockActiveCatalogRelease(ctx, tx, expectedActiveReleaseID, expectedConfigVersion)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	productID, offeringID, err := createCatalogProductRows(ctx, tx, active.ID, in)
	if err != nil {
		return CatalogChangeResult{}, err
	}
	version, err := advanceActiveCatalog(ctx, tx, active, "catalog_change.product_create", actorID, ginSafeMetadata{
		"source_release_id": active.ID,
		"product_id":        productID,
		"product_code":      in.ProductCode,
		"transport_code":    in.TransportCode,
		"offering_id":       offeringID,
	})
	if err != nil {
		return CatalogChangeResult{}, err
	}
	return CatalogChangeResult{ReleaseID: active.ID, ConfigVersion: version, Activated: true, SourceReleaseID: active.ID}, nil
}

func createCatalogProductRows(ctx context.Context, tx *sql.Tx, releaseID uint64, in CatalogProductInput) (uint64, uint64, error) {
	if err := validateCatalogProductReferences(ctx, tx, releaseID, in); err != nil {
		return 0, 0, fmt.Errorf("validate product references: %w", err)
	}
	adapterID, err := ensureCatalogAdapter(ctx, tx, in.Adapter)
	if err != nil {
		return 0, 0, fmt.Errorf("ensure adapter implementation: %w", err)
	}
	executionDigest := catalogProductExecutionDigest(in)
	compatibility := in.StateCompatibilityFingerprint
	if compatibility == "" {
		digest := sha256.Sum256([]byte(in.Protocol + "\x00" + in.UpstreamScopeKind + "\x00" + in.UpstreamScopeKey))
		compatibility = hex.EncodeToString(digest[:])
	}
	now := nowUTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_channel_transports(release_id,channel_id,adapter_implementation_id,transport_code,base_url,protocol,request_method,request_path,auth_scheme,execution_fingerprint,state_compatibility_fingerprint,timeout_ms,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, releaseID, in.ChannelID, adapterID, in.TransportCode, in.BaseURL, in.Protocol, in.RequestMethod, in.RequestPath, in.AuthScheme, executionDigest, compatibility, in.TransportTimeoutMS, now)
	if err != nil {
		return 0, 0, err
	}
	channelTransportID, err := lastID(result)
	if err != nil {
		return 0, 0, err
	}
	hosts, err := catalogAllowedHosts(in)
	if err != nil {
		return 0, 0, err
	}
	for _, host := range hosts {
		if _, err = tx.ExecContext(ctx, `INSERT INTO gw_transport_allowed_hosts(release_id,channel_transport_id,protocol,host_pattern,port,created_at) VALUES (?,?,?,?,?,?)`, releaseID, channelTransportID, host.Protocol, host.Host, host.Port, now); err != nil {
			return 0, 0, err
		}
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO gw_products(release_id,channel_id,product_code,vendor_model,capability_constraints,constraints_schema_version,created_at) VALUES (?,?,?,?,?,?,?)`, releaseID, in.ChannelID, in.ProductCode, in.VendorModel, in.CapabilityConstraints, in.ConstraintsSchemaVersion, now)
	if err != nil {
		return 0, 0, err
	}
	productID, err := lastID(result)
	if err != nil {
		return 0, 0, err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO gw_product_transports(release_id,product_id,channel_transport_id,task_scope,cancel_mode,source_url_policy,upstream_scope_kind,upstream_scope_key,execution_fingerprint,state_compatibility_fingerprint,timeout_ms,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, releaseID, productID, channelTransportID, in.TaskScope, in.CancelMode, in.SourceURLPolicy, in.UpstreamScopeKind, in.UpstreamScopeKey, executionDigest, compatibility, in.TaskTimeoutMS, now)
	if err != nil {
		return 0, 0, err
	}
	productTransportID, err := lastID(result)
	if err != nil {
		return 0, 0, err
	}
	for _, action := range in.Actions {
		if _, err = tx.ExecContext(ctx, `INSERT INTO gw_product_transport_actions(release_id,product_transport_id,action_code,allowed_source_state,idempotency_mode,request_schema_version,response_schema_version,created_at) VALUES (?,?,?,?,?,?,?,?)`, releaseID, productTransportID, action.ActionCode, action.AllowedSourceState, action.IdempotencyMode, action.RequestSchemaVersion, action.ResponseSchemaVersion, now); err != nil {
			return 0, 0, err
		}
	}
	commercial := sha256.Sum256([]byte(strings.Join([]string{in.ProductCode, strconv.FormatUint(in.CredentialPoolID, 10), in.CostPlanCode, executionDigest}, "\x00")))
	entitlement := sha256.Sum256([]byte(strings.Join([]string{strconv.FormatUint(in.ChannelID, 10), in.VendorModel, in.Protocol, in.UpstreamScopeKind, in.UpstreamScopeKey}, "\x00")))
	result, err = tx.ExecContext(ctx, `INSERT INTO gw_offerings(release_id,product_transport_id,credential_pool_id,entitlement_fingerprint,commercial_fingerprint,cost_plan_code,created_at) VALUES (?,?,?,?,?,?,?)`, releaseID, productTransportID, in.CredentialPoolID, hex.EncodeToString(entitlement[:]), hex.EncodeToString(commercial[:]), in.CostPlanCode, now)
	if err != nil {
		return 0, 0, err
	}
	offeringID, err := lastID(result)
	if err != nil {
		return 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_offering_runtime_state(release_id,offering_id,state,state_version,reason_code,updated_at) VALUES (?,?,'active',1,'admin_create',?)`, releaseID, offeringID, now); err != nil {
		return 0, 0, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO gw_offering_state_events(release_id,offering_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,1,NULL,'active','admin_create',?)`, releaseID, offeringID, now); err != nil {
		return 0, 0, err
	}
	result, err = tx.ExecContext(ctx, `INSERT INTO gw_cost_plans(release_id,offering_id,plan_code,created_at) VALUES (?,?,?,?)`, releaseID, offeringID, in.CostPlanCode, now)
	if err != nil {
		return 0, 0, err
	}
	if _, err = lastID(result); err != nil {
		return 0, 0, err
	}
	for _, route := range in.Routes {
		if _, err = tx.ExecContext(ctx, `INSERT INTO gw_routes(release_id,sku_id,offering_id,priority,weight,created_at) VALUES (?,?,?,?,?,?)`, releaseID, route.SKUID, offeringID, route.Priority, route.Weight, now); err != nil {
			return 0, 0, err
		}
	}
	return productID, offeringID, nil
}

func validateCatalogProductReferences(ctx context.Context, tx *sql.Tx, releaseID uint64, in CatalogProductInput) error {
	var channelStatus, poolStatus string
	var poolChannel uint64
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gateway_channels WHERE id=? FOR SHARE`, in.ChannelID).Scan(&channelStatus); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT channel_id,status FROM gw_credential_pools WHERE id=? FOR SHARE`, in.CredentialPoolID).Scan(&poolChannel, &poolStatus); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if channelStatus != "active" || poolStatus != "active" || poolChannel != in.ChannelID {
		return ErrConflict
	}
	for _, route := range in.Routes {
		var count uint64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_skus WHERE id=? AND release_id=?`, route.SKUID, releaseID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return ErrConflict
		}
	}
	return nil
}

func ensureCatalogAdapter(ctx context.Context, tx *sql.Tx, in CatalogAdapterInput) (uint64, error) {
	var id uint64
	var digest, minimum string
	err := tx.QueryRowContext(ctx, `SELECT id,implementation_digest,minimum_semantic_version FROM gw_adapter_implementations WHERE adapter_code=? AND contract_version=?`, in.Code, in.Version).Scan(&id, &digest, &minimum)
	if err == sql.ErrNoRows {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO gw_adapter_implementations(adapter_code,contract_version,implementation_digest,minimum_semantic_version,created_at) VALUES (?,?,?,?,?)`, in.Code, in.Version, in.ImplementationDigest, in.MinimumSemanticVersion, nowUTC())
		if insertErr != nil {
			return 0, insertErr
		}
		return lastID(result)
	}
	if err != nil {
		return 0, err
	}
	if digest != in.ImplementationDigest || minimum != in.MinimumSemanticVersion {
		return 0, fmt.Errorf("%w: %s@%d database identity (%s, %s) differs from runtime identity (%s, %s)", ErrConflict,
			in.Code, in.Version, digest, minimum, in.ImplementationDigest, in.MinimumSemanticVersion)
	}
	return id, nil
}

func catalogAllowedHosts(in CatalogProductInput) ([]CatalogAllowedHostInput, error) {
	return normalizeCatalogAllowedHosts(in.BaseURL, in.AllowedHosts)
}

func normalizeCatalogAllowedHosts(baseURL string, allowedHosts []CatalogAllowedHostInput) ([]CatalogAllowedHostInput, error) {
	base, err := validateCatalogBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	port := uint16(443)
	if base.Scheme == "http" {
		port = 80
	}
	if base.Port() != "" {
		parsed, _ := strconv.ParseUint(base.Port(), 10, 16)
		port = uint16(parsed)
	}
	values := append([]CatalogAllowedHostInput{{Protocol: base.Scheme, Host: strings.ToLower(base.Hostname()), Port: port}}, allowedHosts...)
	seen := make(map[string]string, len(values))
	out := make([]CatalogAllowedHostInput, 0, len(values))
	for _, value := range values {
		value.Protocol = strings.ToLower(strings.TrimSpace(value.Protocol))
		value.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value.Host), "."))
		if value.Port == 0 || value.Protocol != "http" && value.Protocol != "https" || !validExactRemoteHost(value.Host) {
			return nil, ErrInvalidInput
		}
		key := value.Host + "\x00" + strconv.FormatUint(uint64(value.Port), 10)
		if previous, exists := seen[key]; exists {
			if previous != value.Protocol {
				return nil, ErrInvalidInput
			}
			continue
		}
		seen[key] = value.Protocol
		out = append(out, value)
	}
	if len(out) > 32 {
		return nil, ErrInvalidInput
	}
	return out, nil
}

func catalogProductExecutionDigest(in CatalogProductInput) string {
	canonicalAdapter := struct {
		Code                   string `json:"code"`
		Version                uint32 `json:"version"`
		Protocol               string `json:"protocol"`
		MinimumSemanticVersion string `json:"minimum_semantic_version"`
		ImplementationDigest   string `json:"implementation_digest"`
	}{
		Code: in.Adapter.Code, Version: in.Adapter.Version, Protocol: in.Adapter.Protocol,
		MinimumSemanticVersion: in.Adapter.MinimumSemanticVersion, ImplementationDigest: in.Adapter.ImplementationDigest,
	}
	canonical, _ := json.Marshal(ginSafeMetadata{
		"adapter": canonicalAdapter, "base_url": in.BaseURL, "protocol": in.Protocol, "request_method": in.RequestMethod,
		"request_path": in.RequestPath, "auth_scheme": in.AuthScheme, "task_scope": in.TaskScope,
		"cancel_mode": in.CancelMode, "source_url_policy": in.SourceURLPolicy, "constraints": json.RawMessage(in.CapabilityConstraints),
	})
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:])
}
