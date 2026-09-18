package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// cloneParent is a foreign key that must be rewritten: the referenced row lives
// in the source release and its clone has a different AUTO_INCREMENT id.
type cloneParent struct {
	column string
	table  string
}

// cloneLayer describes one release-scoped table. columns are copied byte for
// byte; parents are remapped through the id maps built by earlier layers; keyed
// says later layers need this table's own old-id -> new-id map.
type cloneLayer struct {
	table   string
	parents []cloneParent
	columns []string
	keyed   bool
}

// catalogCloneLayers is the topological order of the release content graph. It
// covers exactly the tables catalogDigestQueries hashes, because the digest is
// the definition of "the content of a release": a table the digest covers but
// the clone skips would be configuration silently dropped from the fork, and one
// the clone copies but the digest ignores would not be content at all.
// catalog_clone_test.go locks that correspondence.
var catalogCloneLayers = []cloneLayer{
	{
		table:   "gw_catalog_models",
		columns: []string{"model_id", "display_name", "description", "capability_tags", "sort_order", "visibility"},
		keyed:   true,
	},
	{
		table:   "gw_catalog_model_names",
		parents: []cloneParent{{"catalog_model_id", "gw_catalog_models"}},
		columns: []string{"model_id", "model_name_id", "is_primary"},
	},
	{
		table:   "gw_model_operations",
		parents: []cloneParent{{"catalog_model_id", "gw_catalog_models"}},
		columns: []string{"operation_contract_id", "normalization_version", "semantic_digest"},
		keyed:   true,
	},
	{
		table:   "gw_skus",
		parents: []cloneParent{{"model_operation_id", "gw_model_operations"}},
		columns: []string{"sku_code", "variant_code", "delivery_mode", "max_results", "idempotency_mode", "service_tiers"},
		keyed:   true,
	},
	{
		table:   "gw_sku_downstream_paths",
		parents: []cloneParent{{"sku_id", "gw_skus"}},
		columns: []string{"path"},
	},
	{
		table:   "gw_sell_rates",
		parents: []cloneParent{{"sku_id", "gw_skus"}},
		// rate_evidence_review_event_id is copied, not re-created: CreateCatalogRate
		// reads the price from accepted evidence and CatalogRateInput carries no
		// price at all, so a clone that minted fresh evidence would be inventing a
		// second review of a price nobody reviewed twice. Only a rate the operator
		// actually edits needs new evidence.
		columns: append([]string{"rate_evidence_review_event_id", "unit_code", "unit_price", "currency_code", "currency_version"}, rateComponentColumns...),
	},
	{
		table: "gw_channel_transports",
		columns: []string{"channel_id", "adapter_implementation_id", "transport_code", "base_url", "protocol",
			"request_method", "request_path", "auth_scheme", "execution_fingerprint",
			"state_compatibility_fingerprint", "timeout_ms"},
		keyed: true,
	},
	{
		table:   "gw_transport_allowed_hosts",
		parents: []cloneParent{{"channel_transport_id", "gw_channel_transports"}},
		columns: []string{"protocol", "host_pattern", "port"},
	},
	{
		table:   "gw_products",
		columns: []string{"channel_id", "product_code", "vendor_model", "capability_constraints", "constraints_schema_version"},
		keyed:   true,
	},
	{
		table: "gw_product_transports",
		parents: []cloneParent{
			{"product_id", "gw_products"},
			{"channel_transport_id", "gw_channel_transports"},
		},
		columns: []string{"task_scope", "cancel_mode", "source_url_policy", "upstream_scope_kind",
			"upstream_scope_key", "execution_fingerprint", "state_compatibility_fingerprint", "timeout_ms"},
		keyed: true,
	},
	{
		table:   "gw_product_transport_actions",
		parents: []cloneParent{{"product_transport_id", "gw_product_transports"}},
		columns: []string{"action_code", "allowed_source_state", "idempotency_mode", "request_schema_version", "response_schema_version"},
	},
	{
		table:   "gw_offerings",
		parents: []cloneParent{{"product_transport_id", "gw_product_transports"}},
		columns: []string{"credential_pool_id", "entitlement_fingerprint", "commercial_fingerprint", "cost_plan_code"},
		keyed:   true,
	},
	{
		table:   "gw_cost_plans",
		parents: []cloneParent{{"offering_id", "gw_offerings"}},
		columns: []string{"plan_code"},
		keyed:   true,
	},
	{
		table:   "gw_cost_rates",
		parents: []cloneParent{{"cost_plan_id", "gw_cost_plans"}},
		columns: append([]string{"rate_evidence_review_event_id", "unit_code", "unit_price", "currency_code", "currency_version"}, rateComponentColumns...),
	},
	{
		table: "gw_routes",
		parents: []cloneParent{
			{"sku_id", "gw_skus"},
			{"offering_id", "gw_offerings"},
		},
		columns: []string{"priority", "weight"},
	},
}

// rateComponentColumns is shared because a sell rate and a cost rate carry the
// same metering shape, and a column added to one but not the other in a future
// migration would be a bug in the migration, not in the clone.
var rateComponentColumns = []string{"component_code", "quantity_source", "charge_event", "unit_scale",
	"quantity_step", "max_quantity", "pricing_mode", "pricing_expr", "max_price"}

// ForkCatalogRelease clones a published release into a fresh draft and returns
// the new release id. This is step ① of the edit cycle: every operator change to
// a live catalog starts as a fork, because a published release is immutable and
// its content_hash is what readiness compares against.
//
// The clone is deliberately faithful rather than normalized — same prices, same
// weights, same fingerprints, same accepted evidence — so a fork with no edits
// describes exactly the behaviour that is running now. Its content_hash still
// differs from the source: the digest covers primary keys, the clone has new
// ones, and uq_gw_catalog_releases_content_hash requires them to differ anyway.
func (s *Store) ForkCatalogRelease(ctx context.Context, tx *sql.Tx, sourceID uint64, in CatalogDraftInput, actorID uint64) (uint64, error) {
	if tx == nil || sourceID == 0 || actorID == 0 || in.Validate() != nil {
		return 0, ErrInvalidInput
	}
	var status string
	// FOR SHARE, not FOR UPDATE: the fork only reads the source, and a published
	// release cannot change under it. The lock stops a concurrent RetireRelease
	// from retiring the thing being copied halfway through.
	if err := tx.QueryRowContext(ctx, s.forShare(`SELECT status FROM gw_catalog_releases WHERE id=?`), sourceID).Scan(&status); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	// A draft is copyable in principle, but forking one has no meaning the
	// operator would want: a draft is already editable in place.
	if status != "published" {
		return 0, ErrConflict
	}
	target, err := s.CreateCatalogDraft(ctx, tx, in, actorID)
	if err != nil {
		return 0, err
	}
	if err := cloneCatalogContent(ctx, tx, sourceID, target); err != nil {
		return 0, err
	}
	// A clone that cannot be published is not a usable starting point, and the
	// operator should learn that here rather than after editing on top of it.
	if err := CheckCatalogStructure(ctx, tx, target); err != nil {
		return 0, fmt.Errorf("forked release %d is structurally incomplete: %w", target, err)
	}
	if err := CheckCatalogPricing(ctx, tx, target); err != nil {
		return 0, fmt.Errorf("forked release %d is not priced completely: %w", target, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO gw_catalog_release_state_events(release_id,old_state,new_state,reason_code,created_at) VALUES (?,NULL,'draft','catalog_fork',?)`, target, nowUTC()); err != nil {
		return 0, fmt.Errorf("record catalog fork: %w", err)
	}
	return target, recordCatalogAdminChange(ctx, tx, actorID, "unified.catalog.fork", "catalog_release", target, ginSafeMetadata{
		"source_release_id": sourceID, "semantic_version": in.SemanticVersion,
	})
}

// cloneCatalogContent walks the layers in topological order, building each
// table's old-id -> new-id map before the layers that reference it are copied.
func cloneCatalogContent(ctx context.Context, tx *sql.Tx, source, target uint64) error {
	ids := make(map[string]map[uint64]uint64, len(catalogCloneLayers))
	for _, layer := range catalogCloneLayers {
		mapped, err := cloneCatalogLayer(ctx, tx, layer, source, target, ids)
		if err != nil {
			return err
		}
		if layer.keyed {
			ids[layer.table] = mapped
		}
	}
	return cloneOfferingRuntimeState(ctx, tx, source, target, ids["gw_offerings"])
}

// clonedRow is one source row: its own primary key, its parent keys in the
// source's id space, and the opaque payload columns.
type clonedRow struct {
	id      uint64
	parents []uint64
	values  []any
}

// insertClonedRows writes the collected layer into the target release and, when
// later layers depend on this one, returns the old-id -> new-id map.
func insertClonedRows(ctx context.Context, tx *sql.Tx, layer cloneLayer, target uint64, ids map[string]map[uint64]uint64, collected []clonedRow) (map[uint64]uint64, error) {
	written := make([]string, 0, len(layer.parents)+len(layer.columns)+2)
	written = append(written, "release_id")
	for _, parent := range layer.parents {
		written = append(written, parent.column)
	}
	written = append(written, layer.columns...)
	written = append(written, "created_at")
	statement := `INSERT INTO ` + layer.table + `(` + strings.Join(written, ",") + `) VALUES (` +
		strings.TrimSuffix(strings.Repeat("?,", len(written)), ",") + `)`
	var mapped map[uint64]uint64
	if layer.keyed {
		mapped = make(map[uint64]uint64, len(collected))
	}
	now := nowUTC()
	for _, row := range collected {
		arguments := make([]any, 0, len(written))
		arguments = append(arguments, target)
		for index, parent := range layer.parents {
			// A missing parent means the source graph is not internally consistent,
			// which the composite foreign keys make impossible in MySQL. Reporting it
			// is still cheaper than inserting a row pointing at id 0.
			newID, ok := ids[parent.table][row.parents[index]]
			if !ok {
				return nil, fmt.Errorf("%w: %s row %d references %s %d, which was not cloned",
					ErrConflict, layer.table, row.id, parent.table, row.parents[index])
			}
			arguments = append(arguments, newID)
		}
		arguments = append(arguments, row.values...)
		arguments = append(arguments, now)
		result, err := tx.ExecContext(ctx, statement, arguments...)
		if err != nil {
			return nil, fmt.Errorf("clone %s row %d: %w", layer.table, row.id, err)
		}
		if !layer.keyed {
			continue
		}
		newID, err := lastID(result)
		if err != nil {
			return nil, err
		}
		mapped[row.id] = newID
	}
	return mapped, nil
}

// cloneCatalogLayer copies one table from the source release into the target
// draft, rewriting every parent id through the maps earlier layers produced. It
// reads the whole layer before writing any of it: the transaction owns a single
// connection, and holding a result set open across the inserts would deadlock on
// MySQL and fail outright on the sqlite test driver.
func cloneCatalogLayer(ctx context.Context, tx *sql.Tx, layer cloneLayer, source, target uint64, ids map[string]map[uint64]uint64) (map[uint64]uint64, error) {
	selected := make([]string, 0, len(layer.parents)+len(layer.columns)+1)
	selected = append(selected, "id")
	for _, parent := range layer.parents {
		selected = append(selected, parent.column)
	}
	selected = append(selected, layer.columns...)
	rows, err := tx.QueryContext(ctx, `SELECT `+strings.Join(selected, ",")+` FROM `+layer.table+` WHERE release_id=? ORDER BY id`, source)
	if err != nil {
		return nil, fmt.Errorf("read %s of release %d: %w", layer.table, source, err)
	}
	var collected []clonedRow
	for rows.Next() {
		row := clonedRow{parents: make([]uint64, len(layer.parents)), values: make([]any, len(layer.columns))}
		targets := make([]any, 0, len(selected))
		targets = append(targets, &row.id)
		for index := range row.parents {
			targets = append(targets, &row.parents[index])
		}
		// NullString, not RawBytes: RawBytes is nil for both a SQL NULL and an empty
		// string, and collapsing '' into NULL would break every NOT NULL column the
		// catalog has (gw_catalog_models.description is routinely empty). It also
		// copies, so the values stay valid after the next Next().
		raw := make([]sql.NullString, len(layer.columns))
		for index := range raw {
			targets = append(targets, &raw[index])
		}
		if err := rows.Scan(targets...); err != nil {
			rows.Close()
			return nil, fmt.Errorf("read %s row of release %d: %w", layer.table, source, err)
		}
		for index, value := range raw {
			// A nullable fingerprint turned into an empty string would change the
			// content digest, so NULL is preserved as an untyped nil argument.
			if !value.Valid {
				continue
			}
			row.values[index] = value.String
		}
		collected = append(collected, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return insertClonedRows(ctx, tx, layer, target, ids, collected)
}

// cloneOfferingRuntimeState carries over the operational state of every cloned
// offering. It is not part of the digest — routing state is not catalog content —
// but routing joins gw_offering_runtime_state with state='active'
// (routing/unified.go), so a fork without it would publish, activate, and then
// serve nothing.
//
// The state itself is copied so a drained offering stays drained, while the
// version restarts at 1 with a single 'catalog_fork' event: the cloned offering
// is a new row that has undergone no transitions, and replaying the source's
// event history here would assert state changes that never happened to it.
func cloneOfferingRuntimeState(ctx context.Context, tx *sql.Tx, source, target uint64, offerings map[uint64]uint64) error {
	if len(offerings) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT offering_id,state,reason_code FROM gw_offering_runtime_state WHERE release_id=? ORDER BY offering_id`, source)
	if err != nil {
		return fmt.Errorf("read offering runtime state of release %d: %w", source, err)
	}
	defer rows.Close()
	type runtimeState struct {
		offering uint64
		state    string
		reason   string
	}
	var states []runtimeState
	for rows.Next() {
		var state runtimeState
		if err := rows.Scan(&state.offering, &state.state, &state.reason); err != nil {
			return err
		}
		states = append(states, state)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	now := nowUTC()
	for _, state := range states {
		offeringID, ok := offerings[state.offering]
		if !ok {
			return fmt.Errorf("%w: offering %d has runtime state but was not cloned", ErrConflict, state.offering)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_offering_runtime_state(release_id,offering_id,state,state_version,reason_code,updated_at) VALUES (?,?,?,1,'catalog_fork',?)`,
			target, offeringID, state.state, now); err != nil {
			return fmt.Errorf("clone runtime state of offering %d: %w", state.offering, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO gw_offering_state_events(release_id,offering_id,state_version,old_state,new_state,reason_code,created_at) VALUES (?,?,1,NULL,?,'catalog_fork',?)`,
			target, offeringID, state.state, now); err != nil {
			return fmt.Errorf("record forked state of offering %d: %w", state.offering, err)
		}
	}
	return nil
}
