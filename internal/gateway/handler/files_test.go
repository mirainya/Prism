package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/config"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fileSQLLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

var filesTestDBSequence atomic.Uint64

func (l *fileSQLLog) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buffer, format, args...)
}

func (l *fileSQLLog) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buffer.Reset()
}

func (l *fileSQLLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buffer.String()
}

func setupFilesTestDB(t *testing.T, sqlLog *fileSQLLog) (*gorm.DB, *model.Token) {
	t.Helper()
	gormConfig := &gorm.Config{}
	if sqlLog != nil {
		gormConfig.Logger = logger.New(sqlLog, logger.Config{LogLevel: logger.Info})
	}
	dsn := fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"), filesTestDBSequence.Add(1))
	db, err := gorm.Open(sqlite.Open(dsn), gormConfig)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Token{}, &model.AIFile{}); err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	token := &model.Token{UserID: 7, Key: "files-test-key", Name: "files-test", Status: 1}
	if err := db.Create(token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}
	model.SetDB(db)
	return db, token
}

func filesTestRouter(token *model.Token) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextKeyTokenID, token.ID)
		c.Set(middleware.ContextKeyToken, token)
		c.Next()
	})
	router.GET("/files", ListFiles)
	router.GET("/files/:id", GetFile)
	router.GET("/files/:id/content", GetFileContent)
	router.POST("/files", UploadFile)
	router.DELETE("/files/:id", DeleteFile)
	return router
}

func TestListFilesUsesMetadataOnlyAndPaginates(t *testing.T) {
	sqlLog := &fileSQLLog{}
	db, token := setupFilesTestDB(t, sqlLog)
	createdAt := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	for _, id := range []string{"file_a", "file_b", "file_c"} {
		record := model.AIFile{ID: id, UserID: token.UserID, TokenID: token.ID, Filename: id + ".txt", Purpose: "user_data", Bytes: 1024, MimeType: "text/plain", Content: bytes.Repeat([]byte("x"), 1024), Status: "processed", CreatedAt: createdAt}
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("create file %s: %v", id, err)
		}
	}
	router := filesTestRouter(token)

	sqlLog.Reset()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/files?limit=2&order=desc", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	var firstPage struct {
		Data    []fileObject `json:"data"`
		HasMore bool         `json:"has_more"`
		FirstID string       `json:"first_id"`
		LastID  string       `json:"last_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &firstPage); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(firstPage.Data) != 2 || firstPage.Data[0].ID != "file_c" || firstPage.Data[1].ID != "file_b" || !firstPage.HasMore || firstPage.FirstID != "file_c" || firstPage.LastID != "file_b" {
		t.Fatalf("unexpected first page: %+v", firstPage)
	}
	if strings.Contains(strings.ToLower(sqlLog.String()), "`content`") {
		t.Fatalf("list query selected content BLOB: %s", sqlLog.String())
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/files?limit=2&order=desc&after=file_b", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("second page status = %d, body=%s", response.Code, response.Body.String())
	}
	var secondPage struct {
		Data    []fileObject `json:"data"`
		HasMore bool         `json:"has_more"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &secondPage); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(secondPage.Data) != 1 || secondPage.Data[0].ID != "file_a" || secondPage.HasMore {
		t.Fatalf("unexpected second page: %+v", secondPage)
	}
}

func TestGetFileMetadataDoesNotSelectContent(t *testing.T) {
	sqlLog := &fileSQLLog{}
	db, token := setupFilesTestDB(t, sqlLog)
	record := model.AIFile{ID: "file_metadata", UserID: token.UserID, TokenID: token.ID, Filename: "metadata.txt", Purpose: "user_data", Bytes: 6, MimeType: "text/plain", Content: []byte("secret"), Status: "processed", CreatedAt: time.Now()}
	if err := db.Create(&record).Error; err != nil {
		t.Fatalf("create file: %v", err)
	}
	router := filesTestRouter(token)

	sqlLog.Reset()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/files/file_metadata", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(strings.ToLower(sqlLog.String()), "`content`") {
		t.Fatalf("metadata query selected content BLOB: %s", sqlLog.String())
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/files/file_metadata/content", nil))
	if response.Code != http.StatusOK || response.Body.String() != "secret" {
		t.Fatalf("content status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestConcurrentUploadsRespectTokenQuota(t *testing.T) {
	_, token := setupFilesTestDB(t, nil)
	previousConfig := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{MaxTotalSizeMB: 1}}
	t.Cleanup(func() { config.C = previousConfig })
	router := filesTestRouter(token)
	payload := bytes.Repeat([]byte("a"), 600*1024)

	start := make(chan struct{})
	statuses := make(chan int, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			part, err := writer.CreateFormFile("file", "upload.bin")
			if err != nil {
				t.Errorf("create multipart file: %v", err)
				return
			}
			if _, err := part.Write(payload); err != nil {
				t.Errorf("write multipart file: %v", err)
				return
			}
			if err := writer.WriteField("purpose", "user_data"); err != nil {
				t.Errorf("write purpose: %v", err)
				return
			}
			if err := writer.Close(); err != nil {
				t.Errorf("close multipart writer: %v", err)
				return
			}
			request := httptest.NewRequest(http.MethodPost, "/files", body)
			request.Header.Set("Content-Type", writer.FormDataContentType())
			<-start
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			statuses <- response.Code
		}()
	}
	close(start)
	wait.Wait()
	close(statuses)

	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusBadRequest] != 1 {
		t.Fatalf("unexpected upload statuses: %v", counts)
	}
	var storedBytes int64
	if err := model.DB().Model(&model.AIFile{}).Select("COALESCE(SUM(bytes), 0)").Scan(&storedBytes).Error; err != nil {
		t.Fatalf("sum files: %v", err)
	}
	if storedBytes != int64(len(payload)) {
		t.Fatalf("stored bytes = %d, want %d", storedBytes, len(payload))
	}
}

func TestListFilesDefaultsToOpenAILimit(t *testing.T) {
	db, token := setupFilesTestDB(t, nil)
	records := make([]model.AIFile, 101)
	for i := range records {
		records[i] = model.AIFile{
			ID:        fmt.Sprintf("file_default_%03d", i),
			UserID:    token.UserID,
			TokenID:   token.ID,
			Filename:  fmt.Sprintf("%03d.txt", i),
			Purpose:   "user_data",
			Bytes:     1,
			MimeType:  "text/plain",
			Content:   []byte{},
			Status:    "processed",
			CreatedAt: time.Unix(int64(i), 0),
		}
	}
	if err := db.Create(&records).Error; err != nil {
		t.Fatalf("create files: %v", err)
	}
	router := filesTestRouter(token)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/files", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}

	var page struct {
		Data    []fileObject `json:"data"`
		HasMore bool         `json:"has_more"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(page.Data) != len(records) || page.HasMore {
		t.Fatalf("default page returned %d files with has_more=%v, want %d and false", len(page.Data), page.HasMore, len(records))
	}
}

func TestSupportedFilePurposes(t *testing.T) {
	for _, purpose := range []string{"assistants", "batch", "evals", "fine-tune", "user_data", "vision"} {
		if !supportedFilePurpose(purpose) {
			t.Errorf("purpose %q should be supported", purpose)
		}
	}
	if supportedFilePurpose("unknown") {
		t.Fatal("unknown purpose should not be supported")
	}
}

func TestConcurrentUploadAndDeleteDoNotDeadlock(t *testing.T) {
	db, token := setupFilesTestDB(t, nil)
	previousConfig := config.C
	config.C = &config.Config{FileStorage: config.FileStorageConfig{MaxTotalSizeMB: 1}}
	t.Cleanup(func() { config.C = previousConfig })

	const operations = 8
	for i := 0; i < operations/2; i++ {
		record := model.AIFile{
			ID:        fmt.Sprintf("file_delete_%d", i),
			UserID:    token.UserID,
			TokenID:   token.ID,
			Filename:  "delete.txt",
			Purpose:   "user_data",
			Bytes:     1,
			MimeType:  "text/plain",
			Content:   []byte("x"),
			Status:    "processed",
			CreatedAt: time.Now(),
		}
		if err := db.Create(&record).Error; err != nil {
			t.Fatalf("create file: %v", err)
		}
	}

	router := filesTestRouter(token)
	start := make(chan struct{})
	statuses := make(chan int, operations)
	var wait sync.WaitGroup
	for i := 0; i < operations; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			var request *http.Request
			if index%2 == 0 {
				body := &bytes.Buffer{}
				writer := multipart.NewWriter(body)
				part, err := writer.CreateFormFile("file", "upload.txt")
				if err != nil {
					statuses <- 0
					return
				}
				_, _ = part.Write([]byte("upload"))
				_ = writer.WriteField("purpose", "user_data")
				_ = writer.Close()
				request = httptest.NewRequest(http.MethodPost, "/files", body)
				request.Header.Set("Content-Type", writer.FormDataContentType())
			} else {
				request = httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/files/file_delete_%d", index/2), nil)
			}
			<-start
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			statuses <- response.Code
		}(i)
	}
	close(start)
	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent upload and delete timed out")
	}
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("operation status = %d, want 200", status)
		}
	}
}
