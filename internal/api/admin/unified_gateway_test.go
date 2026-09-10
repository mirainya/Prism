package admin

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestUnifiedGatewayOverviewReportsLegacyAndTargetCounts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"CREATE TABLE gw_channels (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_abilities (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gateway_channels (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_models (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_credentials (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_catalog_releases (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_api_calls (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_catalog_runtime_state (id INTEGER PRIMARY KEY, active_release_id INTEGER, active_deployment_generation_id INTEGER, state_version INTEGER)",
		"CREATE TABLE gw_deployment_generations (id INTEGER PRIMARY KEY, status TEXT, generation_no INTEGER)",
		"CREATE TABLE gw_offerings (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_routes (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_sell_rates (id INTEGER PRIMARY KEY)",
		"CREATE TABLE gw_cost_rates (id INTEGER PRIMARY KEY)",
		"CREATE TABLE billing_currency_definitions (id INTEGER PRIMARY KEY)",
		"INSERT INTO gw_catalog_runtime_state VALUES (1,NULL,NULL,1)",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO gw_channels(id) VALUES (1),(2); INSERT INTO gw_abilities(id) VALUES (1)").Error; err != nil {
		t.Fatal(err)
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	t.Cleanup(func() { model.SetDB(previous) })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/overview", UnifiedGatewayOverview)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/overview", nil))
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, `"state":"migration_pending"`) || !strings.Contains(got, `"ready_for_cutover":false`) || !strings.Contains(got, `"channels":2`) || !strings.Contains(got, `"abilities":1`) || !strings.Contains(got, `"sell_rates_missing"`) {
		t.Fatalf("unexpected response=%s", got)
	}
	if err := db.Exec("INSERT INTO gw_deployment_generations VALUES (3,'active',1),(8,'preparing',2)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE gw_catalog_runtime_state SET active_deployment_generation_id=3 WHERE id=1").Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/overview", nil))
	if got := w.Body.String(); w.Code != 200 || !strings.Contains(got, `"deployment_id":3`) || !strings.Contains(got, `"latest_generation_no":2`) {
		t.Fatalf("latest draft must not hide the active deployment: %s", got)
	}
	if err := db.Exec("DROP TABLE gw_channels; DROP TABLE gw_abilities").Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/overview", nil))
	if got := w.Body.String(); w.Code != 200 || !strings.Contains(got, `"legacy":{"abilities":0,"channels":0,"data_present":false}`) {
		t.Fatalf("cleaned legacy tables must remain observable: status=%d body=%s", w.Code, got)
	}
	if err := db.Exec("DROP TABLE gw_cost_rates").Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/overview", nil))
	if w.Code != 500 {
		t.Fatalf("missing schema must not look like an empty catalog: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUnifiedGatewayRejectsInvalidPagination(t *testing.T) {
	for _, query := range []string{
		"page=0", "page=-1", "page=abc", "page_size=0", "page_size=101",
		"page=" + strconv.Itoa(int(^uint(0)>>1)) + "&page_size=100",
	} {
		t.Run(query, func(t *testing.T) {
			r := gin.New()
			r.GET("/page", func(c *gin.Context) {
				if _, _, ok := unifiedGatewayPagination(c); ok {
					c.Status(200)
				}
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/page?"+query, nil))
			if w.Code != 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
