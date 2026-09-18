package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func sellRateChangeRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	router.POST("/catalog-changes/sell-rate", ChangeUnifiedSellRate)
	return router
}

const sellRateChangeBody = `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"seedance-2.0-standard","component_code":"generated_second","unit_price":"0.30"}`

// The chain itself is pinned by TestMySQLChangeSellRateForksEditsAndPublishes,
// which needs a real MySQL because every step locks with a literal FOR UPDATE.
// What is left for the handler is the decisions it makes on its own: what it
// refuses before opening a transaction, and what it refuses to be told.
func TestSellRateChangeRejectsBodiesBeforeTakingTheRuntimeLock(t *testing.T) {
	for name, body := range map[string]string{
		// Without the base guard two admins fork divergent branches from the same
		// release and the second silently discards the first (SPEC §5.3).
		"no base guard":     `{"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1"}`,
		"no version":        `{"expected_active_release_id":7,"sku_code":"a","component_code":"b","unit_price":"1"}`,
		"no sku":            `{"expected_active_release_id":7,"semantic_version":"1.0.1","component_code":"b","unit_price":"1"}`,
		"no component":      `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","unit_price":"1"}`,
		"negative price":    `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"-1"}`,
		"unparsable price":  `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"free"}`,
		"no price":          `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b"}`,
		"flat with formula": `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1","pricing_expr":"1+1"}`,
		"formula missing":   `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1","pricing_mode":"expression"}`,
		"unknown mode":      `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1","pricing_mode":"tiered"}`,
		// The semantic digest describes which adapters this binary carries, so it is
		// read from the process and a caller offering one is refused rather than
		// believed.
		"caller-supplied digest": `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1","semantic_digest":"deadbeef"}`,
		// Nothing here may be a way to reach the rate's metering columns: changing
		// what is counted is a different change with different evidence.
		"charge event":  `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1","charge_event":"accepted"}`,
		"release id":    `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1","release_id":9}`,
		"empty body":    `{}`,
		"trailing json": sellRateChangeBody + ` {}`,
		"null":          `null`,
	} {
		response := httptest.NewRecorder()
		sellRateChangeRouter(7).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/sell-rate", strings.NewReader(body)))
		// A 400 here rather than a 500 also proves no transaction was opened: the
		// test installs no database, so anything reaching the store would panic or
		// fail differently.
		if response.Code != 400 {
			t.Fatalf("%s: code=%d body=%s", name, response.Code, response.Body)
		}
	}
}

// Checked after validation but before the store, so an unauthenticated caller
// cannot use a well-formed price change to probe which releases exist.
func TestSellRateChangeRequiresAdmin(t *testing.T) {
	response := httptest.NewRecorder()
	sellRateChangeRouter(0).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/sell-rate", strings.NewReader(sellRateChangeBody)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

// An expression change is accepted by the handler and refused later, by the bound
// gate, if the formula cannot be proven — the handler's job is only to see that a
// formula is present when the mode says there should be one.
func TestSellRateChangeAcceptsAWellFormedExpressionBody(t *testing.T) {
	body := `{"expected_active_release_id":7,"semantic_version":"1.0.1","sku_code":"a","component_code":"b","unit_price":"1","pricing_mode":"expression","pricing_expr":"duration * 0.1"}`
	response := httptest.NewRecorder()
	// No admin, so it stops at the authorization check. Reaching 403 rather than
	// 400 is the assertion: the body itself was accepted.
	sellRateChangeRouter(0).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/sell-rate", strings.NewReader(body)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

func costRateChangeRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	router.POST("/catalog-changes/cost-rate", ChangeUnifiedCostRate)
	return router
}

const costRateChangeBody = `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"seedance-2.0-official","plan_code":"official-cny","component_code":"generated_second","unit_price":"0.10"}`

// The cost path validates addressing that is one column wider than sell (product
// and plan instead of sku), and it defends the same §5.2.2 boundary: no way for
// the request to declare its own source_type or authority_level.
func TestCostRateChangeRejectsBodiesBeforeTakingTheRuntimeLock(t *testing.T) {
	for name, body := range map[string]string{
		"no base guard":     `{"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1"}`,
		"no version":        `{"expected_active_release_id":7,"product_code":"a","plan_code":"b","component_code":"c","unit_price":"1"}`,
		"no product":        `{"expected_active_release_id":7,"semantic_version":"1.0.1","plan_code":"b","component_code":"c","unit_price":"1"}`,
		"no plan":           `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","component_code":"c","unit_price":"1"}`,
		"no component":      `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","unit_price":"1"}`,
		"negative price":    `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"-0.05"}`,
		"unparsable price":  `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"free"}`,
		"no price":          `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c"}`,
		"flat with formula": `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","pricing_expr":"1+1"}`,
		"formula missing":   `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","pricing_mode":"expression"}`,
		"unknown mode":      `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","pricing_mode":"tiered"}`,
		// The only labelling the handler is allowed to set is operator_declared, and
		// the request must not be able to override it. DisallowUnknownFields plus the
		// intentionally missing source_type/authority_level on CostRateChange is what
		// stops the caller from asking to be labelled provider_confirmed (§5.2.2).
		"forged source":    `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","source_type":"provider_pricing"}`,
		"forged authority": `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","authority_level":"provider_confirmed"}`,
		// The rate lookup itself may not be smuggled through, either: cost_plan_id
		// is a fork-scoped surrogate the caller could not know.
		"caller-supplied plan id": `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","cost_plan_id":9}`,
		"caller-supplied digest":  `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","semantic_digest":"deadbeef"}`,
		"empty body":              `{}`,
		"trailing json":           costRateChangeBody + ` {}`,
		"null":                    `null`,
		"bad pool code":           `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","pool_code":"with spaces"}`,
	} {
		response := httptest.NewRecorder()
		costRateChangeRouter(7).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/cost-rate", strings.NewReader(body)))
		if response.Code != 400 {
			t.Fatalf("%s: code=%d body=%s", name, response.Code, response.Body)
		}
	}
}

func TestCostRateChangeRequiresAdmin(t *testing.T) {
	response := httptest.NewRecorder()
	costRateChangeRouter(0).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/cost-rate", strings.NewReader(costRateChangeBody)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}

// pool_code is optional and only used when the (product, plan) pair is ambiguous;
// a body carrying a well-formed pool_code has to reach the store, which here
// stops at the authorization check.
func TestCostRateChangeAcceptsOptionalPoolCode(t *testing.T) {
	body := `{"expected_active_release_id":7,"semantic_version":"1.0.1","product_code":"a","plan_code":"b","component_code":"c","unit_price":"1","pool_code":"primary"}`
	response := httptest.NewRecorder()
	costRateChangeRouter(0).ServeHTTP(response, httptest.NewRequest("POST", "/catalog-changes/cost-rate", strings.NewReader(body)))
	if response.Code != 403 {
		t.Fatalf("code=%d body=%s", response.Code, response.Body)
	}
}
