package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

var channelCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)

type ChannelInput struct {
	Code string `json:"channel_code"`
	Name string `json:"display_name"`
}

func (in ChannelInput) Validate() error {
	if !channelCodePattern.MatchString(in.Code) || !validDisplayName(in.Name) {
		return ErrInvalidInput
	}
	return nil
}

func validDisplayName(value string) bool {
	return value != "" && value == strings.TrimSpace(value) && utf8.ValidString(value) && utf8.RuneCountInString(value) <= 128 && !strings.ContainsAny(value, "\x00\n\r\t")
}

func (s *Store) CreateChannel(ctx context.Context, tx *sql.Tx, in ChannelInput, actorID uint64) (uint64, error) {
	if tx == nil || actorID == 0 {
		return 0, ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gateway_channels(channel_code,display_name,status,created_at) VALUES (?,?,'active',?)`, in.Code, in.Name, nowUTC())
	if err != nil {
		return 0, err
	}
	id, err := lastID(result)
	if err != nil {
		return 0, err
	}
	return id, recordCatalogAdminChange(ctx, tx, actorID, "unified.channel.create", "gateway_channel", id, in)
}

type ChannelUpdate struct {
	Name           string `json:"display_name"`
	Status         string `json:"status"`
	ExpectedName   string `json:"expected_display_name"`
	ExpectedStatus string `json:"expected_status"`
}

func (in ChannelUpdate) Validate() error {
	if !validDisplayName(in.Name) || !validDisplayName(in.ExpectedName) || !channelStatus(in.Status) || !channelStatus(in.ExpectedStatus) {
		return ErrInvalidInput
	}
	return nil
}

func channelStatus(value string) bool { return value == "active" || value == "disabled" }

func (s *Store) UpdateChannel(ctx context.Context, tx *sql.Tx, id uint64, in ChannelUpdate, actorID uint64) error {
	if tx == nil || id == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return err
	}
	var name, status string
	if err := tx.QueryRowContext(ctx, `SELECT display_name,status FROM gateway_channels WHERE id=? FOR UPDATE`, id).Scan(&name, &status); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if name != in.ExpectedName || status != in.ExpectedStatus {
		return ErrConflict
	}
	if name == in.Name && status == in.Status {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE gateway_channels SET display_name=?,status=? WHERE id=?`, in.Name, in.Status, id); err != nil {
		return err
	}
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.channel.update", "gateway_channel", id, in)
}

func recordCatalogAdminChange(ctx context.Context, tx *sql.Tx, actorID uint64, action, resource string, id uint64, metadata any) error {
	body, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_events(actor_type,actor_user_id,action,resource_type,resource_id,outcome,http_status,metadata,created_at) VALUES ('user',?,?,?,?,'success',200,?,?)`, actorID, action, resource, strconv.FormatUint(id, 10), string(body), nowUTC())
	if err != nil {
		return fmt.Errorf("record gateway configuration audit: %w", err)
	}
	return nil
}

type PoolUpdate struct {
	Name         string  `json:"display_name"`
	RequestLimit *uint64 `json:"request_limit"`
	TaskLimit    *uint64 `json:"task_limit"`
	// CostGroupRatio scales what this pool is assumed to cost upstream. Nil
	// leaves it untouched, so a caller that predates the column keeps working.
	// It never reaches a user-facing price: a pool is chosen during routing, and
	// a price that moved with the pool would make the same call cost different
	// amounts and would defeat the pre-request upper-bound proof.
	CostGroupRatio  *string `json:"cost_group_ratio"`
	ExpectedVersion uint64  `json:"expected_version"`
}

func (in PoolUpdate) Validate() error {
	if !validDisplayName(in.Name) || in.ExpectedVersion == 0 || !validPoolLimit(in.RequestLimit) || !validPoolLimit(in.TaskLimit) {
		return ErrInvalidInput
	}
	return validCostGroupRatio(in.CostGroupRatio)
}

// validCostGroupRatio mirrors ck_gw_credential_pools_cost_group_ratio. The ratio
// is fixed-point like every other money-adjacent value (SPEC invariant 10 names
// multipliers), and zero is rejected as well as negative: a zero ratio would
// silently report every upstream call as free.
func validCostGroupRatio(value *string) error {
	if value == nil {
		return nil
	}
	ratio, err := billing.ParseAmount(*value, 8, true)
	if err != nil || ratio.Cmp(billing.Zero()) == 0 {
		return ErrInvalidInput
	}
	return nil
}

func validPoolLimit(value *uint64) bool { return value == nil || *value > 0 && *value <= 1000000 }

func (s *Store) UpdateCredentialPool(ctx context.Context, tx *sql.Tx, id uint64, in PoolUpdate, actorID uint64) error {
	if tx == nil || id == 0 || actorID == 0 {
		return ErrInvalidInput
	}
	if err := in.Validate(); err != nil {
		return err
	}
	var status string
	var version uint64
	if err := tx.QueryRowContext(ctx, `SELECT status,config_version FROM gw_credential_pools WHERE id=? FOR UPDATE`, id).Scan(&status, &version); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if status != "active" || version != in.ExpectedVersion {
		return ErrConflict
	}
	// COALESCE keeps the stored ratio when the caller omitted the field, so the
	// column does not need a separate statement or a read-modify-write.
	if _, err := tx.ExecContext(ctx, `UPDATE gw_credential_pools SET display_name=?,request_limit=?,task_limit=?,cost_group_ratio=COALESCE(?,cost_group_ratio),config_version=config_version+1,updated_at=? WHERE id=?`, in.Name, nullableUint64(in.RequestLimit), nullableUint64(in.TaskLimit), in.CostGroupRatio, nowUTC(), id); err != nil {
		return err
	}
	return recordCatalogAdminChange(ctx, tx, actorID, "unified.pool.update", "credential_pool", id, in)
}

func (s *Store) CreateManagedCredentialPool(ctx context.Context, tx *sql.Tx, in PoolInput, actorID uint64) (uint64, error) {
	if tx == nil || actorID == 0 || in.ChannelID == 0 || !channelCodePattern.MatchString(in.PoolCode) || !validDisplayName(in.DisplayName) || !validPoolLimit(in.RequestLimit) || !validPoolLimit(in.TaskLimit) {
		return 0, ErrInvalidInput
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM gateway_channels WHERE id=? FOR SHARE`, in.ChannelID).Scan(&status); err == sql.ErrNoRows {
		return 0, ErrNotFound
	} else if err != nil {
		return 0, err
	}
	if status != "active" {
		return 0, ErrConflict
	}
	id, err := s.CreateCredentialPool(ctx, tx, in)
	if err != nil {
		return 0, err
	}
	return id, recordCatalogAdminChange(ctx, tx, actorID, "unified.pool.create", "credential_pool", id, in)
}
