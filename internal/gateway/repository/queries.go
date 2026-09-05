package repository

import (
	"context"
	"database/sql"
	"fmt"
)

type PageRequest struct{ Page, PageSize int }

func (p PageRequest) normalized() (int, int, error) {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.PageSize <= 0 {
		p.PageSize = 20
	}
	if p.PageSize > 100 || p.Page > 10_000_000 {
		return 0, 0, ErrInvalidInput
	}
	return p.Page, p.PageSize, nil
}

type CallRow struct {
	ID                                       uint64
	PublicID, Status, Currency, DeliveryMode string
	UserID, TokenID                          uint64
	CreatedAt                                sql.NullTime
}
type CallPage struct {
	Items          []CallRow
	Page, PageSize int
	Total          int64
}

func (s *Store) ListCalls(ctx context.Context, req PageRequest, userID *uint64) (CallPage, error) {
	page, size, err := req.normalized()
	if err != nil {
		return CallPage{}, err
	}
	offset := (page - 1) * size
	var total int64
	var countErr error
	if userID == nil {
		countErr = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_api_calls`).Scan(&total)
	} else {
		countErr = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_api_calls WHERE user_id=?`, *userID).Scan(&total)
	}
	if countErr != nil {
		return CallPage{}, countErr
	}
	query := `SELECT id,public_id,user_id,token_id,status,price_currency,delivery_mode,created_at FROM gw_api_calls ORDER BY id DESC LIMIT ? OFFSET ?`
	args := []any{size, offset}
	if userID != nil {
		query = `SELECT id,public_id,user_id,token_id,status,price_currency,delivery_mode,created_at FROM gw_api_calls WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`
		args = []any{*userID, size, offset}
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return CallPage{}, fmt.Errorf("list calls: %w", err)
	}
	defer rows.Close()
	out := CallPage{Page: page, PageSize: size, Total: total}
	for rows.Next() {
		var r CallRow
		if err := rows.Scan(&r.ID, &r.PublicID, &r.UserID, &r.TokenID, &r.Status, &r.Currency, &r.DeliveryMode, &r.CreatedAt); err != nil {
			return CallPage{}, err
		}
		out.Items = append(out.Items, r)
	}
	if err := rows.Err(); err != nil {
		return CallPage{}, err
	}
	return out, nil
}

type ResourceRow struct {
	ID                      uint64
	PublicID, Kind          string
	CallID, UserID, TokenID uint64
	CreatedAt               sql.NullTime
}
type ResourcePage struct {
	Items          []ResourceRow
	Page, PageSize int
	Total          int64
}

func (s *Store) ListResources(ctx context.Context, req PageRequest, userID *uint64, kind string) (ResourcePage, error) {
	if kind != "" && kind != "capability_task" && kind != "response" && kind != "video_task" && kind != "file" {
		return ResourcePage{}, ErrInvalidInput
	}
	page, size, err := req.normalized()
	if err != nil {
		return ResourcePage{}, err
	}
	offset := (page - 1) * size
	where := ""
	var args []any
	if userID != nil {
		where = " WHERE user_id=?"
		args = append(args, *userID)
	}
	if kind != "" {
		if where == "" {
			where = " WHERE resource_kind=?"
		} else {
			where += " AND resource_kind=?"
		}
		args = append(args, kind)
	}
	var total int64
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gw_api_resources`+where, args...).Scan(&total); err != nil {
		return ResourcePage{}, err
	}
	query := `SELECT id,public_id,resource_kind,call_id,user_id,token_id,created_at FROM gw_api_resources` + where + ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, size, offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return ResourcePage{}, err
	}
	defer rows.Close()
	out := ResourcePage{Page: page, PageSize: size, Total: total}
	for rows.Next() {
		var r ResourceRow
		if err := rows.Scan(&r.ID, &r.PublicID, &r.Kind, &r.CallID, &r.UserID, &r.TokenID, &r.CreatedAt); err != nil {
			return ResourcePage{}, err
		}
		out.Items = append(out.Items, r)
	}
	return out, rows.Err()
}
