package admin

import (
	"net/http/httptest"
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
		"CREATE TABLE gw_catalog_runtime_state (id INTEGER PRIMARY KEY, active_release_id INTEGER, state_version INTEGER)",
		"CREATE TABLE gw_deployment_generations (id INTEGER PRIMARY KEY, status TEXT)",
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
	if got := w.Body.String(); !strings.Contains(got, `"state":"legacy_runtime"`) || !strings.Contains(got, `"channels":2`) || !strings.Contains(got, `"abilities":1`) {
		t.Fatalf("unexpected response=%s", got)
	}
}
