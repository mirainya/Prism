package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/credentials"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
)

func unifiedChannelError(c *gin.Context, err error) {
	var mysqlErr *mysql.MySQLError
	switch {
	case errors.Is(err, repository.ErrDuplicateCredentialSecret):
		resp.ErrorMsg(c, http.StatusConflict, 409, "密钥已存在于该渠道")
	case errors.Is(err, repository.ErrCredentialEncryptionUnavailable):
		resp.ErrorMsg(c, http.StatusServiceUnavailable, 503, "凭据加密配置不可用，无法保存密钥")
	case errors.Is(err, repository.ErrInvalidInput):
		resp.ErrorMsg(c, http.StatusBadRequest, 400, "配置字段无效")
	case errors.Is(err, repository.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		resp.ErrorMsg(c, http.StatusNotFound, 404, "配置不存在")
	case errors.Is(err, repository.ErrConflict):
		resp.ErrorMsg(c, http.StatusConflict, 409, "配置状态已变化或仍有任务占用，请刷新后重试")
	case errors.As(err, &mysqlErr) && mysqlErr.Number == 1062:
		resp.ErrorMsg(c, http.StatusConflict, 409, "标识已存在")
	default:
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, "读取或保存网关配置失败")
	}
}

func unifiedChannelBody(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16384)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return false
	}
	return true
}

func UnifiedChannels(c *gin.Context) {
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	search := strings.TrimSpace(c.Query("q"))
	status := c.Query("status")
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > 128 || status != "" && status != "active" && status != "disabled" {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	where := ` WHERE (?='' OR c.status=?) AND (?='' OR LOCATE(?,c.channel_code)>0 OR LOCATE(?,c.display_name)>0)`
	args := []any{status, status, search, search, search}
	var total int64
	ctx := c.Request.Context()
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gateway_channels c`+where, args...).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT c.id,c.channel_code,c.display_name,c.status,c.created_at,(SELECT COUNT(*) FROM gw_credential_pools p WHERE p.channel_id=c.id),(SELECT COUNT(*) FROM gw_credentials k WHERE k.channel_id=c.id) FROM gateway_channels c`+where+` ORDER BY c.id DESC LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var id, pools, keys uint64
		var code, name, state string
		var created sql.NullTime
		if err := rows.Scan(&id, &code, &name, &state, &created, &pools, &keys); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": id, "channel_code": code, "display_name": name, "status": state, "created_at": nullableTime(created), "pool_count": pools, "credential_count": keys})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedChannel(c *gin.Context) {
	var in repository.ChannelInput
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Code = strings.TrimSpace(in.Code)
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateChannel(c.Request.Context(), tx, in, actor)
	})
}

func UpdateUnifiedChannel(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.ChannelUpdate
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(id), store.UpdateChannel(c.Request.Context(), tx, uint64(id), in, actor)
	})
}

func unifiedChannelWrite(c *gin.Context, action func(*repository.Store, *sql.Tx, uint64) (uint64, error)) {
	actor := uint64(currentAdminID(c))
	if actor == 0 {
		resp.ErrorMsg(c, 403, 403, "需要管理员身份")
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	store, err := repository.New(db)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	var id uint64
	err = store.WithTx(c.Request.Context(), func(tx *sql.Tx) error { var e error; id, e = action(store, tx, actor); return e })
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, gin.H{"id": id})
}

func UnifiedChannelPools(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	page, size, ok := unifiedGatewayPagination(c)
	if !ok {
		return
	}
	db, err := model.DB().DB()
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	ctx := c.Request.Context()
	var total int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM gateway_channels WHERE id=?`, id).Scan(new(uint64)); err != nil {
		unifiedChannelError(c, err)
		return
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_credential_pools WHERE channel_id=?`, id).Scan(&total); err != nil {
		unifiedChannelError(c, err)
		return
	}
	rows, err := db.QueryContext(ctx, `SELECT p.id,p.pool_code,p.display_name,p.status,p.config_version,p.request_limit,p.task_limit,(SELECT COUNT(*) FROM gw_credentials k WHERE k.credential_pool_id=p.id) FROM gw_credential_pools p WHERE p.channel_id=? ORDER BY p.id DESC LIMIT ? OFFSET ?`, id, size, (page-1)*size)
	if err != nil {
		unifiedChannelError(c, err)
		return
	}
	defer rows.Close()
	items := make([]gin.H, 0, size)
	for rows.Next() {
		var poolID, version, count uint64
		var code, name, status string
		var requests, tasks sql.NullInt64
		if err := rows.Scan(&poolID, &code, &name, &status, &version, &requests, &tasks, &count); err != nil {
			unifiedChannelError(c, err)
			return
		}
		items = append(items, gin.H{"id": poolID, "channel_id": id, "pool_code": code, "display_name": name, "status": status, "config_version": version, "request_limit": nullableInt(requests), "task_limit": nullableInt(tasks), "credential_count": count})
	}
	if err := rows.Err(); err != nil {
		unifiedChannelError(c, err)
		return
	}
	resp.Success(c, unifiedGatewayPage{Items: items, Page: page, PageSize: size, Total: total})
}

func CreateUnifiedChannelPool(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in struct {
		Code         string  `json:"pool_code"`
		Name         string  `json:"display_name"`
		RequestLimit *uint64 `json:"request_limit"`
		TaskLimit    *uint64 `json:"task_limit"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return store.CreateManagedCredentialPool(c.Request.Context(), tx, repository.PoolInput{ChannelID: uint64(id), PoolCode: strings.TrimSpace(in.Code), DisplayName: strings.TrimSpace(in.Name), RequestLimit: in.RequestLimit, TaskLimit: in.TaskLimit}, actor)
	})
}

func UpdateUnifiedPool(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in repository.PoolUpdate
	if !unifiedChannelBody(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		return uint64(id), store.UpdateCredentialPool(c.Request.Context(), tx, uint64(id), in, actor)
	})
}

func TransitionUnifiedPool(c *gin.Context) {
	id, err := resp.ParseUintParam(c, "id")
	if err != nil {
		return
	}
	var in struct {
		Status          credentials.PoolState `json:"status"`
		ExpectedVersion uint64                `json:"expected_version"`
	}
	if !unifiedChannelBody(c, &in) {
		return
	}
	if in.ExpectedVersion == 0 || in.Status != credentials.PoolDraining && in.Status != credentials.PoolDisabled {
		unifiedChannelError(c, repository.ErrInvalidInput)
		return
	}
	unifiedChannelWrite(c, func(store *repository.Store, tx *sql.Tx, actor uint64) (uint64, error) {
		var status credentials.PoolState
		var version uint64
		if err := tx.QueryRowContext(c.Request.Context(), `SELECT status,config_version FROM gw_credential_pools WHERE id=? FOR UPDATE`, id).Scan(&status, &version); err != nil {
			return 0, err
		}
		if version != in.ExpectedVersion || credentials.TransitionPool(status, in.Status) != nil {
			return 0, repository.ErrConflict
		}
		return uint64(id), store.TransitionCredentialPool(c.Request.Context(), tx, uint64(id), status, in.Status, "admin:"+strconv.FormatUint(actor, 10))
	})
}
