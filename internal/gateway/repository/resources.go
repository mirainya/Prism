package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type ResourceInput struct {
	PublicID, Kind          string
	CallID, UserID, TokenID uint64
}

func (s *Store) CreateResource(ctx context.Context, tx *sql.Tx, in ResourceInput) (uint64, error) {
	if tx == nil || in.PublicID == "" || len(in.PublicID) > 36 || in.CallID == 0 || in.UserID == 0 || in.TokenID == 0 {
		return 0, ErrInvalidInput
	}
	if in.Kind != "capability_task" && in.Kind != "response" && in.Kind != "video_task" && in.Kind != "file" {
		return 0, ErrInvalidInput
	}
	var callID uint64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM gw_api_calls WHERE id=? AND user_id=? AND token_id=? FOR SHARE`, in.CallID, in.UserID, in.TokenID).Scan(&callID); err == sql.ErrNoRows {
		return 0, ErrConflict
	} else if err != nil {
		return 0, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_api_resources(public_id,resource_kind,call_id,user_id,token_id,created_at) VALUES (?,?,?,?,?,?)`, in.PublicID, in.Kind, in.CallID, in.UserID, in.TokenID, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("create resource: %w", err)
	}
	return lastID(result)
}

type CapabilityTaskInput struct {
	ResourceID     uint64
	TaskNo, Status string
	Progress       uint8
	PromptPreview  string
	Parameters     any
}

func (s *Store) CreateCapabilityTask(ctx context.Context, tx *sql.Tx, in CapabilityTaskInput) error {
	if tx == nil || in.ResourceID == 0 || in.TaskNo == "" || len(in.TaskNo) > 36 || in.Status == "" || len(in.Status) > 24 || in.Progress > 100 || len(in.PromptPreview) > 2000 {
		return ErrInvalidInput
	}
	if err := ensureResourceKind(ctx, tx, in.ResourceID, "capability_task"); err != nil {
		return err
	}
	params, err := jsonValue(in.Parameters)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_capability_tasks(resource_id,task_no,status,progress,prompt_preview,parameter_summary,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, in.ResourceID, in.TaskNo, in.Status, in.Progress, in.PromptPreview, params, nowUTC(), nowUTC())
	return err
}

type VideoTaskInput struct {
	ResourceID     uint64
	TaskNo, Status string
	Progress       uint8
	Specification  any
}

func (s *Store) CreateVideoTask(ctx context.Context, tx *sql.Tx, in VideoTaskInput) error {
	if tx == nil || in.ResourceID == 0 || in.TaskNo == "" || len(in.TaskNo) > 36 || in.Status == "" || len(in.Status) > 24 || in.Progress > 100 {
		return ErrInvalidInput
	}
	if err := ensureResourceKind(ctx, tx, in.ResourceID, "video_task"); err != nil {
		return err
	}
	spec, err := jsonValue(in.Specification)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_video_tasks(resource_id,task_no,status,progress,specification_summary,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, in.ResourceID, in.TaskNo, in.Status, in.Progress, spec, nowUTC(), nowUTC())
	return err
}

type AIResponseInput struct {
	ResourceID         uint64
	ResponseNo, Status string
	PreviousResourceID *uint64
	Summary            any
}

func (s *Store) CreateAIResponse(ctx context.Context, tx *sql.Tx, in AIResponseInput) error {
	if tx == nil || in.ResourceID == 0 || in.ResponseNo == "" || len(in.ResponseNo) > 36 || in.Status == "" || len(in.Status) > 24 {
		return ErrInvalidInput
	}
	if err := ensureResourceKind(ctx, tx, in.ResourceID, "response"); err != nil {
		return err
	}
	if in.PreviousResourceID != nil {
		if err := ensureResourceKind(ctx, tx, *in.PreviousResourceID, "response"); err != nil {
			return err
		}
	}
	summary, err := jsonValue(in.Summary)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO gw_ai_responses(resource_id,response_no,status,previous_response_resource_id,result_summary,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, in.ResourceID, in.ResponseNo, in.Status, nullableID(in.PreviousResourceID), summary, nowUTC(), nowUTC())
	return err
}

func (s *Store) UpdateCapabilityTask(ctx context.Context, tx *sql.Tx, resourceID uint64, status string, progress uint8, parameters any) error {
	if tx == nil || resourceID == 0 || status == "" || len(status) > 24 || progress > 100 {
		return ErrInvalidInput
	}
	if err := ensureResourceKind(ctx, tx, resourceID, "capability_task"); err != nil {
		return err
	}
	v, err := jsonValue(parameters)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_capability_tasks SET status=?,progress=?,parameter_summary=?,updated_at=? WHERE resource_id=?`, status, progress, v, nowUTC(), resourceID)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdateVideoTask(ctx context.Context, tx *sql.Tx, resourceID uint64, status string, progress uint8, specification any) error {
	if tx == nil || resourceID == 0 || status == "" || len(status) > 24 || progress > 100 {
		return ErrInvalidInput
	}
	if err := ensureResourceKind(ctx, tx, resourceID, "video_task"); err != nil {
		return err
	}
	v, err := jsonValue(specification)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE gw_video_tasks SET status=?,progress=?,specification_summary=?,updated_at=? WHERE resource_id=?`, status, progress, v, nowUTC(), resourceID)
	if err != nil {
		return err
	}
	ok, err := affected(res)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

func jsonValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal resource summary: %w", err)
	}
	return string(b), nil
}

func ensureResourceKind(ctx context.Context, tx *sql.Tx, resourceID uint64, expected string) error {
	var kind string
	if err := tx.QueryRowContext(ctx, `SELECT resource_kind FROM gw_api_resources WHERE id=? FOR UPDATE`, resourceID).Scan(&kind); err == sql.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if kind != expected {
		return ErrConflict
	}
	return nil
}
