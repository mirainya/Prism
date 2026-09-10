package admin

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	gatewayruntime "github.com/mirainya/Prism/internal/gateway/runtime"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestUnifiedDeploymentsListIncludesPreparingAndActiveGenerations(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`CREATE TABLE gw_deployment_generations(id INTEGER, generation_no INTEGER, status TEXT, semantic_version TEXT, semantic_digest TEXT, member_frozen_at DATETIME, created_at DATETIME)`,
		`CREATE TABLE gw_deployment_members(id INTEGER, deployment_generation_id INTEGER, instance_id TEXT, role TEXT)`,
	} {
		if err := db.Exec(query).Error; err != nil {
			t.Fatal(err)
		}
	}
	for id, status := range []string{"active", "preparing"} {
		if err := db.Exec(`INSERT INTO gw_deployment_generations VALUES (?,?,?,'1.0','digest',NULL,?)`, id+1, id+1, status, time.Now().UTC()).Error; err != nil {
			t.Fatal(err)
		}
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	t.Cleanup(func() { model.SetDB(previous) })
	router := gin.New()
	router.GET("/deployments", ListUnifiedDeployments)
	identity, err := gatewayruntime.CurrentProcessIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_deployment_members VALUES (1,1,?,?),(2,1,'other','api-worker')`, identity.InstanceID, identity.Role).Error; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		query, status string
		members       int
	}{
		{"?page=1&page_size=1", "preparing", 0},
		{"?page=2&page_size=1", "active", 2},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/deployments"+test.query, nil))
		var result struct {
			Data struct {
				Total int `json:"total"`
				Items []struct {
					Status          string `json:"status"`
					Members         int    `json:"member_count"`
					CurrentMemberID int    `json:"current_member_id"`
				} `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || result.Data.Total != 2 || len(result.Data.Items) != 1 || result.Data.Items[0].Status != test.status || result.Data.Items[0].Members != test.members || test.members > 0 && result.Data.Items[0].CurrentMemberID != 1 {
			t.Fatalf("unexpected page: status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
