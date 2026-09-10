//go:build integration

package api_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/mirainya/Prism/internal/api/admin"
	"github.com/mirainya/Prism/internal/api/console"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/migrate"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/pkg/auth"
	"github.com/mirainya/Prism/pkg/cache"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestUnifiedConsoleMySQLBrowser(t *testing.T) {
	dsn, redisAddr := os.Getenv("PRISM_CONSOLE_TEST_DSN"), os.Getenv("PRISM_CONSOLE_TEST_REDIS")
	if dsn == "" || redisAddr == "" {
		t.Skip("isolated MySQL, Redis and Playwright are required")
	}
	my, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	for _, addr := range []string{my.Addr, redisAddr} {
		host, _, err := net.SplitHostPort(addr)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			t.Fatal("only isolated loopback servers are permitted")
		}
	}
	if my.Net != "tcp" {
		t.Fatal("test MySQL must use TCP")
	}
	my.DBName = ""
	server, err := sql.Open("mysql", my.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	name := fmt.Sprintf("prism_test_console_%d", time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := server.Exec("DROP DATABASE `" + name + "`"); err != nil {
			t.Error(err)
		}
	}()
	my.DBName, my.ParseTime, my.MultiStatements = name, true, true
	db, err := gorm.Open(mysql.Open(my.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if _, err := migrate.Up(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var oldDB *gorm.DB
	if model.HasDB() {
		oldDB = model.DB()
	}
	model.SetDB(db)
	defer model.SetDB(oldDB)
	oldCache, oldConfig := cache.Client, config.C
	config.C = &config.Config{}
	config.C.Server.JWTSecret = randomConsoleSecret(t)
	cache.Client = redis.NewClient(&redis.Options{Addr: redisAddr})
	defer func() { cache.Client.Close(); cache.Client, config.C = oldCache, oldConfig }()
	if err := cache.Client.Ping(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
	password := randomConsoleSecret(t)
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	user := model.User{Username: "qa-admin", Password: hash, Role: model.UserRoleAdmin, Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	defer cache.DeleteUserLoginTokens(context.Background(), user.ID)
	t.Setenv("PRISM_GATEWAY_KEK_B64", randomConsoleSecret(t))
	t.Setenv("PRISM_GATEWAY_HMAC_B64", randomConsoleSecret(t))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery())
	console.RegisterAuthRoutes(router.Group("/api/auth"))
	console.RegisterPublicRoutes(router.Group("/api/public"))
	console.RegisterRoutes(router.Group("/api", middleware.JWTAuth()))
	admin.RegisterRoutes(router.Group("/api/admin", middleware.JWTAuth(), middleware.AdminOnly()))
	root, err := filepath.Abs("../../console")
	if err != nil {
		t.Fatal(err)
	}
	static := http.FileServer(http.Dir(filepath.Join(root, "dist")))
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Status(404)
			return
		}
		static.ServeHTTP(c.Writer, c.Request)
	})
	httpServer := httptest.NewServer(router)
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", filepath.Join(root, "tests", "unified-credentials.cjs"))
	command.Dir = root
	command.Env = append(os.Environ(), "PRISM_TEST_BASE_URL="+httpServer.URL, "PRISM_TEST_PASSWORD="+password)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func randomConsoleSecret(t *testing.T) string {
	t.Helper()
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(value[:])
}
