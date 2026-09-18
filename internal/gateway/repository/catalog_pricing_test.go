package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestCatalogPricingRequiresCompleteReleaseSpecificRates(t *testing.T) {
	for _, test := range []struct {
		name, change string
		ready        bool
	}{
		{"complete", "", true},
		{"explicit_free", `UPDATE gw_sell_rates SET unit_price=0`, true},
		{"no_skus", `DELETE FROM gw_skus`, false},
		{"unpriced_sku", `INSERT INTO gw_skus(id,release_id) VALUES (2,1)`, false},
		{"another_release_price", `UPDATE gw_sell_rates SET release_id=2`, false},
		{"unconfigured_platform_currency", `DELETE FROM billing_system_state`, false},
		{"wrong_platform_currency", `UPDATE billing_system_state SET currency_code='CNY'`, false},
		{"retired_currency", `UPDATE billing_currency_definitions SET status='retired'`, false},
		{"unreviewed_price", `UPDATE gw_rate_evidence_review_events SET decision='submitted'`, false},
		{"superseded_price_evidence", `UPDATE gw_rate_evidence_review_state SET state='superseded'`, false},
		{"no_offerings", `DELETE FROM gw_offerings`, false},
		{"offering_without_plan", `INSERT INTO gw_offerings VALUES (2,1)`, false},
		{"no_plan", `DELETE FROM gw_cost_plans`, false},
		{"unpriced_plan", `DELETE FROM gw_cost_rates`, true},
		{"additional_unpriced_plan", `INSERT INTO gw_cost_plans VALUES (2,1,1)`, true},
		{"negative_sell_price", `UPDATE gw_sell_rates SET unit_price=-1`, false},
		{"negative_cost_price", `UPDATE gw_cost_rates SET unit_price=-1`, false},
		{"invalid_cost_component", `INSERT INTO gw_cost_rates(id,release_id,cost_plan_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version) VALUES (2,1,1,2,'second',1,'USD',1)`, false},
		{"missing_quantity_source", `UPDATE gw_sell_rates SET quantity_source=NULL`, false},
		{"missing_max_quantity", `UPDATE gw_sell_rates SET max_quantity=NULL`, false},
		{"invalid_unit_source", `UPDATE gw_sell_rates SET unit_code='second'`, false},
		{"invalid_rounding", `UPDATE billing_currency_definitions SET rounding_mode='unknown'`, false},
		{"invalid_extra_sell_component", `INSERT INTO gw_sell_rates(id,release_id,sku_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version) VALUES (2,1,1,1,'unknown',1,'USD',1)`, false},
		{"missing_cost_source", `UPDATE gw_cost_rates SET quantity_source=NULL`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, err := sql.Open("sqlite", ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			db.SetMaxOpenConns(1)
			t.Cleanup(func() { db.Close() })
			for _, query := range []string{
				`CREATE TABLE gw_skus(id INTEGER, release_id INTEGER, variant_code TEXT NOT NULL DEFAULT 'default')`,
				`CREATE TABLE gw_sell_rates(id INTEGER, release_id INTEGER, sku_id INTEGER, rate_evidence_review_event_id INTEGER, unit_code TEXT, unit_price TEXT, currency_code TEXT, currency_version INTEGER, component_code TEXT DEFAULT 'base', quantity_source TEXT DEFAULT 'one', charge_event TEXT DEFAULT 'call.succeeded', unit_scale INTEGER DEFAULT 0, quantity_step TEXT DEFAULT '0', max_quantity TEXT DEFAULT '1', pricing_mode TEXT DEFAULT 'flat', pricing_expr TEXT, max_price TEXT)`,
				`CREATE TABLE billing_system_state(id INTEGER, currency_code TEXT, currency_version INTEGER)`,
				`CREATE TABLE billing_currency_definitions(id INTEGER, currency_code TEXT, definition_version INTEGER, status TEXT, fraction_digits INTEGER, rounding_mode TEXT, max_amount TEXT)`,
				`CREATE TABLE gw_rate_evidence_review_events(id INTEGER, rate_evidence_id INTEGER, decision TEXT)`,
				`CREATE TABLE gw_rate_evidence_review_state(id INTEGER, rate_evidence_id INTEGER, state TEXT, latest_review_event_id INTEGER)`,
				`CREATE TABLE gw_offerings(id INTEGER, release_id INTEGER)`,
				`CREATE TABLE gw_cost_plans(id INTEGER, release_id INTEGER, offering_id INTEGER)`,
				`CREATE TABLE gw_cost_rates(id INTEGER, release_id INTEGER, cost_plan_id INTEGER, rate_evidence_review_event_id INTEGER, unit_code TEXT, unit_price TEXT, currency_code TEXT, currency_version INTEGER, component_code TEXT DEFAULT 'base', quantity_source TEXT DEFAULT 'one', charge_event TEXT DEFAULT 'call.succeeded', unit_scale INTEGER DEFAULT 0, quantity_step TEXT DEFAULT '0', max_quantity TEXT DEFAULT '1', pricing_mode TEXT DEFAULT 'flat', pricing_expr TEXT, max_price TEXT)`,
				`INSERT INTO gw_skus(id,release_id) VALUES (1,1)`,
				`INSERT INTO gw_sell_rates(id,release_id,sku_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version) VALUES (1,1,1,1,'request',1,'USD',1)`,
				`INSERT INTO billing_system_state VALUES (1,'USD',1)`,
				`INSERT INTO billing_currency_definitions VALUES (1,'USD',1,'active',8,'half_even','1000000.000000000000000000')`,
				`INSERT INTO gw_rate_evidence_review_events VALUES (1,1,'accepted'),(2,2,'submitted')`,
				`INSERT INTO gw_rate_evidence_review_state VALUES (1,1,'accepted',1),(2,2,'submitted',2)`,
				`INSERT INTO gw_offerings VALUES (1,1)`,
				`INSERT INTO gw_cost_plans VALUES (1,1,1)`,
				`INSERT INTO gw_cost_rates(id,release_id,cost_plan_id,rate_evidence_review_event_id,unit_code,unit_price,currency_code,currency_version) VALUES (1,1,1,1,'request',0.1,'USD',1)`,
			} {
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if test.change != "" {
				if _, err := db.Exec(test.change); err != nil {
					t.Fatal(err)
				}
			}
			err = CheckCatalogPricing(context.Background(), db, 1)
			if test.ready && err != nil || !test.ready && !errors.Is(err, ErrConflict) {
				t.Fatalf("ready=%v error=%v", test.ready, err)
			}
		})
	}
}
