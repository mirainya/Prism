package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"gorm.io/gorm"
)

func TestUnifiedRequestLogsArePagedScopedAndRedacted(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	for _, statement := range []string{
		`CREATE TABLE gw_api_calls(id INTEGER PRIMARY KEY)`,
		`CREATE TABLE gw_api_call_attempts(id INTEGER PRIMARY KEY,call_id INTEGER,attempt_no INTEGER)`,
		`CREATE TABLE gw_async_executions(id INTEGER PRIMARY KEY,state TEXT)`,
		`CREATE TABLE gw_channel_request_logs(id INTEGER PRIMARY KEY,attempt_id INTEGER,request_seq INTEGER,action TEXT,status TEXT,http_status INTEGER,duration_ms INTEGER,error_code TEXT,request_bytes_complete BOOLEAN,response_bytes_complete BOOLEAN,created_at DATETIME,completed_at DATETIME,async_execution_id INTEGER,request_payload_blob_id INTEGER,response_payload_blob_id INTEGER,secret TEXT)`,
		`CREATE TABLE encrypted_blobs(id INTEGER PRIMARY KEY AUTOINCREMENT,keyring_id INTEGER,purpose TEXT,schema_version INTEGER,aad_hash TEXT,nonce BLOB,ciphertext BLOB,content_hmac TEXT,content_length INTEGER,created_at DATETIME,purged_at DATETIME)`,
		`CREATE TABLE encrypted_blob_key_wraps(id INTEGER PRIMARY KEY AUTOINCREMENT,encrypted_blob_id INTEGER,keyring_id INTEGER,kek_version INTEGER,wrap_nonce BLOB,wrapped_dek BLOB,created_at DATETIME)`,
		`INSERT INTO gw_api_calls VALUES (1),(2)`,
		`INSERT INTO gw_api_call_attempts VALUES (1,1,1),(2,2,1)`,
		`INSERT INTO gw_async_executions VALUES (1,'manual_review')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 4; i++ {
		attempt := 1
		if i == 4 {
			attempt = 2
		}
		if err := db.Exec(`INSERT INTO gw_channel_request_logs(id,attempt_id,request_seq,action,status,http_status,duration_ms,error_code,request_bytes_complete,response_bytes_complete,created_at,completed_at,async_execution_id,secret) VALUES (?,?,?,'query','unknown',503,25,'provider_http_error',1,1,?,NULL,1,'must-not-be-exposed')`, i, attempt, i, time.Now().UTC()).Error; err != nil {
			t.Fatal(err)
		}
	}
	key := bytes.Repeat([]byte{19}, 32)
	t.Setenv("PRISM_GATEWAY_PAYLOAD_KEK_B64", base64.StdEncoding.EncodeToString(key))
	t.Setenv("PRISM_GATEWAY_PAYLOAD_HMAC_B64", base64.StdEncoding.EncodeToString(key))
	store, err := repository.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	err = store.WithTx(context.Background(), func(tx *sql.Tx) error {
		blob := repository.BlobInput{KeyringID: 1, KEKVersion: 1, KEK: key, HMACKey: key}
		blob.Plaintext = []byte(`{"model":"provider-model","prompt":"exact prompt"}`)
		if _, err := store.PutRequestLogPayload(context.Background(), tx, 1, "request", blob); err != nil {
			return err
		}
		blob.Plaintext = []byte(`{"id":"provider-task","status":"queued"}`)
		_, err := store.PutRequestLogPayload(context.Background(), tx, 1, "response", blob)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var previous *gorm.DB
	if model.HasDB() {
		previous = model.DB()
	}
	model.SetDB(db)
	defer model.SetDB(previous)
	router := gin.New()
	router.GET("/calls/:id/requests", UnifiedCallRequestLogs)
	router.GET("/calls/:id/requests/:request_id/payloads", UnifiedRequestLogPayloads)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/calls/1/requests?page=2&page_size=2", nil))
	var data struct {
		Data struct {
			Items []struct {
				ID         int    `json:"id"`
				AsyncState string `json:"async_state"`
			} `json:"items"`
			Total int `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || response.Code != 200 || data.Data.Total != 3 || len(data.Data.Items) != 1 || data.Data.Items[0].ID != 1 || data.Data.Items[0].AsyncState != "manual_review" {
		t.Fatalf("invalid log page: status=%d body=%s err=%v", response.Code, response.Body.String(), err)
	}
	if strings.Contains(response.Body.String(), "must-not-be-exposed") {
		t.Fatal("request list leaked a secret")
	}
	payloadResponse := httptest.NewRecorder()
	router.ServeHTTP(payloadResponse, httptest.NewRequest("GET", "/calls/1/requests/1/payloads", nil))
	if payloadResponse.Code != 200 || !strings.Contains(payloadResponse.Body.String(), `\"provider-model\"`) || !strings.Contains(payloadResponse.Body.String(), `\"provider-task\"`) {
		t.Fatalf("payload detail was not decrypted: status=%d body=%s", payloadResponse.Code, payloadResponse.Body.String())
	}
	foreignResponse := httptest.NewRecorder()
	router.ServeHTTP(foreignResponse, httptest.NewRequest("GET", "/calls/2/requests/1/payloads", nil))
	if foreignResponse.Code != 404 {
		t.Fatalf("cross-call payload lookup returned %d", foreignResponse.Code)
	}
	for _, tc := range []struct {
		path string
		code int
	}{{"/calls/99/requests", 404}, {"/calls/1/requests?page_size=101", 400}, {"/calls/1/requests?page=-1", 400}} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", tc.path, nil))
		if response.Code != tc.code {
			t.Errorf("%s returned %d", tc.path, response.Code)
		}
	}
	if err := db.Exec(`DROP TABLE gw_channel_request_logs`).Error; err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/calls/1/requests", nil))
	if response.Code != 500 {
		t.Fatal("query error was presented as an empty list")
	}
}
