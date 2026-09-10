package engine

import (
	"context"
	"database/sql"
	"errors"

	gatewaybilling "github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/repository"
)

type reservationLifecycle interface {
	Cancel() error
	Settle(*canonical.Usage) error
	Retain() error
}

type unifiedReservation struct {
	store                 *repository.Store
	callID, reservationID uint64
	pricing               gatewaybilling.RateSchedule
}

func (r *unifiedReservation) Cancel() error {
	return r.resolve("released", "reservation_released")
}

func (r *unifiedReservation) Retain() error {
	return r.resolve("unknown_hold", "reservation_held_unknown")
}

func (r *unifiedReservation) Settle(usage *canonical.Usage) error {
	if r == nil || r.store == nil || r.reservationID == 0 {
		return nil
	}
	facts, err := canonicalBillingFacts(usage)
	if err != nil {
		return errors.Join(err, r.Retain())
	}
	charge, err := r.pricing.Evaluate(facts)
	if err != nil {
		return errors.Join(err, r.Retain())
	}
	return r.store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return r.store.SettleReservation(context.Background(), tx, r.reservationID, charge.Amount.String())
	})
}

func (r *unifiedReservation) resolve(target, event string) error {
	if r == nil || r.store == nil || r.reservationID == 0 {
		return nil
	}
	return r.store.WithTx(context.Background(), func(tx *sql.Tx) error {
		return r.store.ResolveReservation(context.Background(), tx, r.reservationID, target, event)
	})
}
