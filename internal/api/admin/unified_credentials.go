package admin

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/credentials"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

func appendUnifiedCredentialPurposes(ctx context.Context, db *sql.DB, items []gin.H) error {
	if len(items) == 0 {
		return nil
	}
	args := make([]any, len(items))
	byID := make(map[int64]gin.H, len(items))
	for i, item := range items {
		id := item["id"].(int64)
		args[i], byID[id] = id, item
		item["purposes"] = []string{}
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(items)), ",")
	rows, err := db.QueryContext(ctx, `SELECT credential_id,purpose FROM gw_credential_purpose_grants WHERE credential_id IN (`+placeholders+`) AND status='active' ORDER BY credential_id,purpose`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var purpose string
		if err := rows.Scan(&id, &purpose); err != nil {
			return err
		}
		item := byID[id]
		item["purposes"] = append(item["purposes"].([]string), purpose)
	}
	return rows.Err()
}

func CreateUnifiedCredential(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in struct {
		Code         string                `json:"credential_code"`
		Secret       string                `json:"secret"`
		RequestLimit *uint64               `json:"request_limit"`
		TaskLimit    *uint64               `json:"task_limit"`
		Weight       uint64                `json:"weight"`
		Purposes     []credentials.Purpose `json:"purposes"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	fields := repository.ManagedCredentialInput{Code: in.Code, Secret: []byte(in.Secret), RequestLimit: in.RequestLimit, TaskLimit: in.TaskLimit, Weight: in.Weight, Purposes: in.Purposes}
	in.Secret = ""
	defer clear(fields.Secret)
	if err := fields.Validate(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateManagedCredentialPlaintext(c.Request.Context(), tx, uint64(id), fields, actor)
	})
}

func UpdateUnifiedCredential(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.CredentialUpdate
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(id), store.UpdateManagedCredential(c.Request.Context(), tx, uint64(id), in, actor)
	})
}

func TransitionUnifiedCredential(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in struct {
		Status          credentials.CredentialState `json:"status"`
		ExpectedVersion uint64                      `json:"expected_version"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	if in.ExpectedVersion == 0 || in.Status != credentials.CredentialDraining && in.Status != credentials.CredentialDisabled {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		var state credentials.CredentialState
		var version uint64
		if err := tx.QueryRowContext(c.Request.Context(), `SELECT status,config_version FROM gw_credentials WHERE id=? FOR UPDATE`, id).Scan(&state, &version); err != nil {
			return 0, err
		}
		if version != in.ExpectedVersion || credentials.TransitionCredential(state, in.Status) != nil {
			return 0, repository.ErrConflict
		}
		return uint64(id), store.TransitionCredential(c.Request.Context(), tx, uint64(id), state, in.Status, "admin:"+strconv.FormatUint(actor, 10))
	})
}
