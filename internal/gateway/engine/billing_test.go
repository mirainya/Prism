package engine

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/canonical"
	"github.com/mirainya/Prism/internal/gateway/routing"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestReservationSQLiteSettleAndCancel(t *testing.T) {
	db, e := gorm.Open(sqlite.Open("file:engine_billing?mode=memory&cache=shared"), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&model.User{}, &model.Token{}, &model.BillingLog{}, &model.BalanceEntry{}); e != nil {
		t.Fatal(e)
	}
	model.SetDB(db)
	u := model.User{Username: "u", Balance: decimal.NewFromInt(1000000), Status: 1}
	db.Create(&u)
	tok := model.Token{UserID: u.ID, Key: "k", Balance: decimal.NewFromInt(1000000), Status: 1}
	db.Create(&tok)
	route := &routing.RouteResult{InputPrice: decimal.NewFromInt(1000000), OutputPrice: decimal.NewFromInt(1000000)}
	max := 2
	r, e := Reserve(service.NewBillingService(), tok.ID, u.ID, route, canonical.Request{MaxOutputTokens: &max}, "one")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Settle(&canonical.Usage{InputTokens: 1, OutputTokens: 2}); e != nil {
		t.Fatal(e)
	}
	var got model.Token
	db.First(&got, tok.ID)
	if !got.Balance.Equal(decimal.NewFromInt(999997)) {
		t.Fatalf("balance %s", got.Balance)
	}
	r, e = Reserve(service.NewBillingService(), tok.ID, u.ID, &routing.RouteResult{PriceMode: "request", InputPrice: decimal.NewFromInt(1)}, canonical.Request{}, "two")
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Cancel(); e != nil {
		t.Fatal(e)
	}
	db.First(&got, tok.ID)
	if !got.Balance.Equal(decimal.NewFromInt(999997)) {
		t.Fatalf("cancel balance %s", got.Balance)
	}
}

func TestEstimateInputTokensDoesNotChargeBase64BytesAsTokens(t *testing.T) {
	base := canonical.Request{Items: []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{
		Type: "input_image", Data: "short", Text: "abcdefgh中文",
	}}}}}
	large := base
	large.Items = []canonical.Item{{Type: "message", Role: canonical.RoleUser, Content: []canonical.Content{{
		Type: "input_image", Data: string(make([]byte, 2*1024*1024)), Text: "abcdefgh中文",
	}}}}
	if got, want := estimateInputTokens(large), estimateInputTokens(base); got != want {
		t.Fatalf("large inline data estimate=%d, want %d", got, want)
	}
	if got := estimateTextTokens("abcdefgh中文"); got != 4 {
		t.Fatalf("text estimate=%d, want 4", got)
	}
}

func TestTokenCostKeepsEightDecimalPlaces(t *testing.T) {
	route := &routing.RouteResult{
		InputPrice:  decimal.RequireFromString("0.01"),
		OutputPrice: decimal.Zero,
	}
	actual := cost(route, 1, 0).RoundCeil(billingPrecision)
	expected := decimal.RequireFromString("0.00000001")
	if !actual.Equal(expected) {
		t.Fatalf("token cost = %s, want %s", actual, expected)
	}
}
