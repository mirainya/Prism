package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func transportAllowedHostsChangeRouter(actor uint) *gin.Engine {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != 0 {
			c.Set("user_id", actor)
		}
	})
	router.POST("/catalog-changes/transport-allowed-hosts", ChangeUnifiedTransportAllowedHosts)
	return router
}

const transportAllowedHostsChangeBody = `{"expected_active_release_id":7,"expected_config_version":9,"transport_code":"video-main","allowed_hosts":[{"protocol":"https","host":"cdn.example.com","port":443}]}`

func TestTransportAllowedHostsChangeRejectsInvalidBodiesBeforeStore(t *testing.T) {
	for name, body := range map[string]string{
		"missing active release": `{"expected_config_version":9,"transport_code":"video-main","allowed_hosts":[]}`,
		"missing config version": `{"expected_active_release_id":7,"transport_code":"video-main","allowed_hosts":[]}`,
		"missing transport":      `{"expected_active_release_id":7,"expected_config_version":9,"allowed_hosts":[]}`,
		"wildcard host":          `{"expected_active_release_id":7,"expected_config_version":9,"transport_code":"video-main","allowed_hosts":[{"protocol":"https","host":"*.example.com","port":443}]}`,
		"private address":        `{"expected_active_release_id":7,"expected_config_version":9,"transport_code":"video-main","allowed_hosts":[{"protocol":"https","host":"127.0.0.1","port":443}]}`,
		"unrestricted mode":      `{"expected_active_release_id":7,"expected_config_version":9,"transport_code":"video-main","allowed_hosts":[],"unrestricted":true}`,
		"unknown protocol":       `{"expected_active_release_id":7,"expected_config_version":9,"transport_code":"video-main","allowed_hosts":[{"protocol":"ftp","host":"cdn.example.com","port":443}]}`,
		"caller digest":          `{"expected_active_release_id":7,"expected_config_version":9,"transport_code":"video-main","allowed_hosts":[],"semantic_digest":"deadbeef"}`,
		"trailing json":          transportAllowedHostsChangeBody + ` {}`,
		"null":                   `null`,
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/catalog-changes/transport-allowed-hosts", strings.NewReader(body))
			transportAllowedHostsChangeRouter(7).ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTransportAllowedHostsChangeRequiresAdminAfterValidation(t *testing.T) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/catalog-changes/transport-allowed-hosts", strings.NewReader(transportAllowedHostsChangeBody))
	transportAllowedHostsChangeRouter(0).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestListUnifiedCatalogTransportAllowedHosts(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	t.Cleanup(func() { model.SetDB(previous) })

	mock.ExpectQuery(`SELECT ct\.transport_code,ct\.base_url,r\.config_version`).
		WithArgs(uint64(7), uint64(31)).
		WillReturnRows(sqlmock.NewRows([]string{"transport_code", "base_url", "config_version"}).
			AddRow("video-main", "https://api.example.com/v1", 9))
	mock.ExpectQuery(`SELECT protocol,host_pattern,port FROM gw_transport_allowed_hosts`).
		WithArgs(uint64(7), uint64(31)).
		WillReturnRows(sqlmock.NewRows([]string{"protocol", "host_pattern", "port"}).
			AddRow("https", "api.example.com", 443).
			AddRow("https", "cdn.example.com", 443))

	router := gin.New()
	router.GET("/catalog/:id/transports/:transport_id/allowed-hosts", ListUnifiedCatalogTransportAllowedHosts)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/catalog/7/transports/31/allowed-hosts", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, expected := range []string{`"transport_code":"video-main"`, `"config_version":9`, `"host":"api.example.com"`, `"host":"cdn.example.com"`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("body=%s missing %s", body, expected)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
