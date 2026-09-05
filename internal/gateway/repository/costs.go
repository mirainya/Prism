package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

type UpstreamCostEvidenceInput struct {
	ChannelID                         uint64
	SourceType, ExternalKey, FactHMAC string
}

func (s *Store) UpsertUpstreamCostEvidence(ctx context.Context, tx *sql.Tx, in UpstreamCostEvidenceInput) (uint64, error) {
	if tx == nil || in.ChannelID == 0 || in.SourceType == "" || in.ExternalKey == "" || !validHexDigest(in.FactHMAC, 32) {
		return 0, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_cost_evidence(channel_id,source_type,external_key,fact_hmac,created_at) VALUES (?,?,?,?,?) ON DUPLICATE KEY UPDATE id=LAST_INSERT_ID(id)`, in.ChannelID, in.SourceType, in.ExternalKey, in.FactHMAC, nowUTC())
	if err != nil {
		return 0, fmt.Errorf("upsert upstream cost evidence: %w", err)
	}
	return lastID(result)
}

type UpstreamCostEventInput struct {
	AttemptID                                  uint64
	ComponentCode                              string
	EventSeq                                   uint64
	Direction                                  string
	Amount, Quantity                           string
	CurrencyCode                               string
	CurrencyVersion                            uint32
	SourceType, ReconciliationState            string
	RequestLogID, ResultDeliveryID, EvidenceID *uint64
}

func (s *Store) InsertUpstreamCostEvent(ctx context.Context, tx *sql.Tx, in UpstreamCostEventInput) (uint64, bool, error) {
	if tx == nil || in.AttemptID == 0 || in.ComponentCode == "" || in.EventSeq == 0 || in.CurrencyCode == "" || in.CurrencyVersion == 0 || in.Amount == "" || in.Quantity == "" {
		return 0, false, ErrInvalidInput
	}
	if in.Direction != "increase" && in.Direction != "decrease" || in.SourceType != "estimated" && in.SourceType != "reported" && in.SourceType != "manual" || in.ReconciliationState != "pending" && in.ReconciliationState != "confirmed" && in.ReconciliationState != "disputed" && in.ReconciliationState != "voided" {
		return 0, false, ErrInvalidInput
	}
	amount, err := billing.ParseAmount(in.Amount, 18, false)
	if err != nil {
		return 0, false, ErrInvalidInput
	}
	quantity, err := billing.ParseAmount(in.Quantity, 18, false)
	if err != nil {
		return 0, false, ErrInvalidInput
	}
	var id uint64
	err = tx.QueryRowContext(ctx, `SELECT id FROM gw_upstream_cost_events WHERE attempt_id=? AND component_code=? AND event_seq=?`, in.AttemptID, in.ComponentCode, in.EventSeq).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if err != sql.ErrNoRows {
		return 0, false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO gw_upstream_cost_events(attempt_id,component_code,event_seq,direction,amount,quantity,currency_code,currency_version,source_type,reconciliation_state,request_log_id,result_delivery_id,evidence_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.AttemptID, in.ComponentCode, in.EventSeq, in.Direction, amount.String(), quantity.String(), in.CurrencyCode, in.CurrencyVersion, in.SourceType, in.ReconciliationState, nullableID(in.RequestLogID), nullableID(in.ResultDeliveryID), nullableID(in.EvidenceID), nowUTC())
	if err != nil {
		return 0, false, fmt.Errorf("insert upstream cost event: %w", err)
	}
	id, err = lastID(result)
	return id, false, err
}
