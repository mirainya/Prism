package service

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/mirainya/Prism/internal/model"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestDashboardStatsUseCallsWithoutCountingRetries(t *testing.T) {
	seed := seedDashboardStats(t)
	service := NewDashboardService()

	userStats, err := service.GetStats(seed.userOne, false)
	if err != nil {
		t.Fatal(err)
	}
	assertTodayStats(t, userStats.Today, 4, 2, 2, 3.25)
	today := seed.today.Format("01-02")
	var todayTrend *DailyStats
	for index := range userStats.WeeklyTrend {
		if userStats.WeeklyTrend[index].Date == today {
			todayTrend = &userStats.WeeklyTrend[index]
			break
		}
	}
	if todayTrend == nil || todayTrend.Requests != 4 || todayTrend.Errors != 2 || math.Abs(todayTrend.Cost-3.25) > 0.0001 {
		t.Fatalf("today trend=%#v", todayTrend)
	}
	userDistribution := capabilityDistribution(userStats.CapabilityDist)
	if userDistribution["image-model"] != 1 || userDistribution["video-model"] != 1 || len(userDistribution) != 2 {
		t.Fatalf("user capability distribution=%v", userDistribution)
	}

	adminStats, err := service.GetStats(0, true)
	if err != nil {
		t.Fatal(err)
	}
	assertTodayStats(t, adminStats.Today, 6, 4, 2, 12.25)
	adminDistribution := capabilityDistribution(adminStats.CapabilityDist)
	if adminDistribution["image-model"] != 2 || adminDistribution["video-model"] != 1 {
		t.Fatalf("admin capability distribution=%v", adminDistribution)
	}

	userEnhanced, err := service.GetChatStats(7, seed.userOne, false)
	if err != nil {
		t.Fatal(err)
	}
	if userEnhanced.TokenUsage.TotalPromptTokens != 0 || userEnhanced.TokenUsage.TotalCompletionTokens != 0 || userEnhanced.TokenUsage.TotalTokens != 0 {
		t.Fatalf("user token usage=%#v", userEnhanced.TokenUsage)
	}
	userRankings := modelRankingMap(userEnhanced.ModelRankings)
	if userRankings["shared-model"].Calls != 2 || userRankings["shared-model"].TotalTokens != 0 || userRankings["response-model"].Calls != 1 || userRankings["video-model"].Calls != 1 {
		t.Fatalf("user model rankings=%v", userRankings)
	}
	userRates := channelRateMap(userEnhanced.ChannelRates)
	assertChannelRate(t, userRates[model.APICallRouteGatewayV2+":0"], 3, 1, 100.0/3.0)
	assertChannelRate(t, userRates[model.APICallRouteCapability+":0"], 1, 1, 100)
	for _, item := range userEnhanced.ChannelRates {
		if item.ChannelID != 0 || item.ChannelType != "" {
			t.Fatalf("non-admin channel details leaked: %#v", item)
		}
	}
	encodedUserStats, err := json.Marshal(userEnhanced)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedUserStats), "channel_id") || strings.Contains(string(encodedUserStats), "channel_type") {
		t.Fatalf("non-admin response contains internal channel fields: %s", encodedUserStats)
	}

	adminEnhanced, err := service.GetChatStats(7, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	adminRates := channelRateMap(adminEnhanced.ChannelRates)
	assertChannelRate(t, adminRates[model.APICallRouteGatewayV2+":10"], 3, 2, 100*2.0/3.0)
	if adminRates[model.APICallRouteGatewayV2+":10"].ChannelType != "gateway-a" {
		t.Fatalf("admin channel details missing: %#v", adminRates[model.APICallRouteGatewayV2+":10"])
	}
	if adminEnhanced.TokenUsage.TotalTokens != 0 {
		t.Fatalf("admin token usage=%#v", adminEnhanced.TokenUsage)
	}
}

func TestListTasksFiltersByModelCodeAndUser(t *testing.T) {
	seed := seedDashboardStats(t)
	service := NewDashboardService()

	userResult, err := service.ListTasks(&ListTasksRequest{
		Page: 1, PageSize: 20, Capability: "image-model",
	}, seed.userOne, false)
	if err != nil {
		t.Fatal(err)
	}
	if userResult.Total != 1 || len(userResult.Items) != 1 || userResult.Items[0].TaskNo != "task-user-image" {
		t.Fatalf("user filtered tasks=%#v", userResult)
	}

	adminResult, err := service.ListTasks(&ListTasksRequest{
		Page: 1, PageSize: 20, Capability: "image-model",
	}, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if adminResult.Total != 2 || len(adminResult.Items) != 2 {
		t.Fatalf("admin filtered tasks=%#v", adminResult)
	}

	tokenResult, err := service.ListTasks(&ListTasksRequest{
		Page: 1, PageSize: 20, Capability: "image-model", TokenID: seed.tokenOne,
	}, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if tokenResult.Total != 1 || tokenResult.Items[0].TaskNo != "task-user-image" {
		t.Fatalf("token filtered tasks=%#v", tokenResult)
	}
}

func TestListTasksKeepsStableSnapshotAcrossPages(t *testing.T) {
	seed := seedDashboardStats(t)
	service := NewDashboardService()

	first, err := service.ListTasks(&ListTasksRequest{Page: 1, PageSize: 1}, seed.userOne, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.SnapshotAt == "" || len(first.Items) != 1 || first.Items[0].TaskNo != "task-user-video" {
		t.Fatalf("first page=%#v", first)
	}
	snapshot, err := time.Parse(time.RFC3339Nano, first.SnapshotAt)
	if err != nil {
		t.Fatal(err)
	}
	if err := createUnifiedDashboardTask(model.DB(), 100, "task-inserted-after-snapshot", seed.userOne, seed.tokenOne, "image-model", "completed", snapshot.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	second, err := service.ListTasks(&ListTasksRequest{
		Page: 2, PageSize: 1, SnapshotAt: first.SnapshotAt,
	}, seed.userOne, false)
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 2 || len(second.Items) != 1 || second.Items[0].TaskNo != "task-user-image" {
		t.Fatalf("second page=%#v", second)
	}
}

func TestUnifiedConsoleCallsUseGatewayLedger(t *testing.T) {
	seed := seedDashboardStats(t)
	result, err := NewAPICallService().ListCalls(&ListCallsRequest{
		Page: 1, PageSize: 20, ActorUserID: seed.userOne,
		Model: "image-model", Status: model.APICallStatusCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != "call-image" || result.Items[0].Model != "image-model" {
		t.Fatalf("unified calls=%#v", result)
	}
}

func TestConversationCallFactsPreferUnifiedLedger(t *testing.T) {
	seed := seedDashboardStats(t)
	db := model.DB()
	if err := db.AutoMigrate(&model.APICall{}); err != nil {
		t.Fatal(err)
	}
	legacy := dashboardCall("call-image", 999, 999, "legacy-model", model.APICallStatusFailed, 0, 0, 0, 0, seed.today)
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	facts, err := loadConversationCallFactsTx(db, "call-image", false)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Call.UserID != seed.userOne || facts.Call.Model != "image-model" {
		t.Fatalf("conversation facts=%#v", facts)
	}
}

type dashboardSeed struct {
	userOne  uint
	tokenOne uint
	today    time.Time
}

func seedDashboardStats(t *testing.T) dashboardSeed {
	t.Helper()
	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE gw_api_calls(id INTEGER PRIMARY KEY, public_id TEXT, user_id INTEGER, token_id INTEGER, operation_contract_id INTEGER, catalog_release_id INTEGER, model_operation_id INTEGER, sku_id INTEGER, status TEXT, quoted_amount TEXT, current_attempt_id INTEGER, final_attempt_id INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE gw_operation_contracts(id INTEGER PRIMARY KEY, operation_code TEXT)`,
		`CREATE TABLE gw_operation_routes(id INTEGER PRIMARY KEY, operation_contract_id INTEGER, route_template TEXT)`,
		`CREATE TABLE gw_models(id INTEGER PRIMARY KEY, model_code TEXT)`,
		`CREATE TABLE gw_catalog_models(id INTEGER PRIMARY KEY, release_id INTEGER, model_id INTEGER, display_name TEXT)`,
		`CREATE TABLE gw_model_operations(id INTEGER PRIMARY KEY, release_id INTEGER, catalog_model_id INTEGER)`,
		`CREATE TABLE gw_api_resources(id INTEGER PRIMARY KEY, public_id TEXT, resource_kind TEXT, call_id INTEGER, user_id INTEGER, token_id INTEGER, created_at DATETIME)`,
		`CREATE TABLE gw_capability_tasks(resource_id INTEGER PRIMARY KEY, task_no TEXT, status TEXT, progress INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE gw_video_tasks(resource_id INTEGER PRIMARY KEY, task_no TEXT, status TEXT, progress INTEGER, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE gw_api_call_attempts(id INTEGER PRIMARY KEY, call_id INTEGER, attempt_no INTEGER, catalog_release_id INTEGER, product_transport_id INTEGER, credential_id INTEGER, state TEXT, created_at DATETIME, updated_at DATETIME)`,
		`CREATE TABLE gw_product_transports(id INTEGER PRIMARY KEY, release_id INTEGER, product_id INTEGER, channel_transport_id INTEGER)`,
		`CREATE TABLE gw_products(id INTEGER PRIMARY KEY, release_id INTEGER, channel_id INTEGER, vendor_model TEXT)`,
		`CREATE TABLE gw_channel_transports(id INTEGER PRIMARY KEY, release_id INTEGER, transport_code TEXT, protocol TEXT, request_path TEXT)`,
		`CREATE TABLE gateway_channels(id INTEGER PRIMARY KEY, display_name TEXT)`,
		`CREATE TABLE billing_reservations(id INTEGER PRIMARY KEY, call_id INTEGER, state TEXT)`,
		`CREATE TABLE billing_settlements(reservation_id INTEGER PRIMARY KEY, actual_amount TEXT)`,
		`CREATE TABLE billing_events(id INTEGER PRIMARY KEY, call_id INTEGER, event_type TEXT, amount TEXT, created_at DATETIME)`,
		`CREATE TABLE gw_channel_request_logs(id INTEGER PRIMARY KEY, attempt_id INTEGER, action TEXT, http_status INTEGER, duration_ms INTEGER, error_code TEXT)`,
		`CREATE TABLE gw_api_call_payloads(id INTEGER PRIMARY KEY, call_id INTEGER, kind TEXT, content_length INTEGER, retention_until DATETIME, purged_at DATETIME, created_at DATETIME)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	model.SetDB(db)

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today := todayStart.Add(time.Minute)
	yesterday := todayStart.Add(-time.Hour)
	userOne := uint(11)
	userTwo := uint(22)
	tokenOne := uint(101)

	if err := db.Exec(`INSERT INTO gw_operation_contracts(id,operation_code) VALUES (1,'test')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_operation_routes(id,operation_contract_id,route_template) VALUES (1,1,'/v1/test')`).Error; err != nil {
		t.Fatal(err)
	}
	models := []string{"shared-model", "response-model", "old-model", "image-model", "video-model"}
	for index, code := range models {
		id := index + 1
		if err := db.Exec(`INSERT INTO gw_models(id,model_code) VALUES (?,?)`, id, code).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO gw_catalog_models(id,release_id,model_id,display_name) VALUES (?,1,?,?)`, id, id, code).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO gw_model_operations(id,release_id,catalog_model_id) VALUES (?,1,?)`, id, id).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO gateway_channels(id,display_name) VALUES (10,'gateway-a'),(20,'gateway-b'),(30,'image')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_products(id,release_id,channel_id,vendor_model) VALUES (10,1,10,'shared'),(20,1,20,'response'),(30,1,30,'image')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_channel_transports(id,release_id,transport_code,protocol,request_path) VALUES (10,1,'openai','openai','/v1/test'),(20,1,'anthropic','anthropic','/v1/test'),(30,1,'image','openai','/images')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO gw_product_transports(id,release_id,product_id,channel_transport_id) VALUES (10,1,10,10),(20,1,20,20),(30,1,30,30)`).Error; err != nil {
		t.Fatal(err)
	}
	type callSeed struct {
		id             int
		user, token    uint
		model, final   int
		public, status string
		cost           float64
		created        time.Time
	}
	calls := []callSeed{
		{1, userOne, tokenOne, 1, 2, "call-chat", "completed", 1, today},
		{2, userOne, tokenOne, 2, 4, "call-response", "failed", .25, today.Add(time.Minute)},
		{3, userOne, tokenOne, 4, 5, "call-image", "completed", 2, today.Add(2 * time.Minute)},
		{4, userOne, tokenOne, 1, 0, "call-yesterday", "completed", 4, yesterday},
		{5, userTwo, 202, 1, 6, "call-other-user", "completed", 9, today.Add(3 * time.Minute)},
		{6, userOne, tokenOne, 3, 7, "call-outside-range", "completed", 20, today.AddDate(0, 0, -10)},
		{7, userOne, tokenOne, 5, 8, "call-video-task", "failed", 0, today.Add(4 * time.Minute)},
		{8, userTwo, 202, 4, 9, "call-admin-image-task", "completed", 0, today.Add(5 * time.Minute)},
	}
	for _, call := range calls {
		updated := call.created.Add(time.Second)
		if err := db.Exec(`INSERT INTO gw_api_calls(id,public_id,user_id,token_id,operation_contract_id,catalog_release_id,model_operation_id,sku_id,status,quoted_amount,final_attempt_id,created_at,updated_at) VALUES (?,?,?,?,1,1,?,1,?,'0',?,?,?)`, call.id, call.public, call.user, call.token, call.model, call.status, call.final, call.created, updated).Error; err != nil {
			t.Fatal(err)
		}
		if call.cost > 0 {
			if err := db.Exec(`INSERT INTO billing_reservations(id,call_id,state) VALUES (?,?,'settled')`, call.id, call.id).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Exec(`INSERT INTO billing_settlements(reservation_id,actual_amount) VALUES (?,?)`, call.id, call.cost).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	attempts := []struct {
		id, call, no, transport int
		state                   string
		created                 time.Time
	}{
		{1, 1, 1, 10, "failed", today}, {2, 1, 2, 10, "completed", today.Add(time.Second)}, {3, 1, 3, 10, "started", today.Add(2 * time.Second)},
		{4, 2, 1, 20, "failed", today.Add(time.Minute)}, {5, 3, 1, 30, "completed", today.Add(2 * time.Minute)},
		{6, 5, 1, 10, "completed", today.Add(3 * time.Minute)}, {7, 6, 1, 10, "completed", today.AddDate(0, 0, -10)},
		{8, 7, 1, 20, "failed", today.Add(4 * time.Minute)}, {9, 8, 1, 30, "completed", today.Add(5 * time.Minute)},
	}
	for _, attempt := range attempts {
		if err := db.Exec(`INSERT INTO gw_api_call_attempts(id,call_id,attempt_no,catalog_release_id,product_transport_id,credential_id,state,created_at,updated_at) VALUES (?,?,?,1,?,1,?,?,?)`, attempt.id, attempt.call, attempt.no, attempt.transport, attempt.state, attempt.created, attempt.created.Add(time.Second)).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, task := range []struct {
		id, call             int
		user, token          uint
		public, kind, status string
		progress             int
		created              time.Time
	}{
		{1, 3, userOne, tokenOne, "task-user-image", "capability_task", "succeeded", 100, today.Add(2 * time.Minute)},
		{2, 7, userOne, tokenOne, "task-user-video", "video_task", "failed", 100, today.Add(4 * time.Minute)},
		{3, 8, userTwo, 202, "task-admin-image", "capability_task", "succeeded", 100, today.Add(5 * time.Minute)},
	} {
		if err := db.Exec(`INSERT INTO gw_api_resources(id,public_id,resource_kind,call_id,user_id,token_id,created_at) VALUES (?,?,?,?,?,?,?)`, task.id, task.public, task.kind, task.call, task.user, task.token, task.created).Error; err != nil {
			t.Fatal(err)
		}
		table := "gw_capability_tasks"
		if task.kind == "video_task" {
			table = "gw_video_tasks"
		}
		if err := db.Exec(`INSERT INTO `+table+`(resource_id,task_no,status,progress,created_at,updated_at) VALUES (?,?,?,?,?,?)`, task.id, task.public, task.status, task.progress, task.created, task.created).Error; err != nil {
			t.Fatal(err)
		}
	}
	return dashboardSeed{userOne: userOne, tokenOne: tokenOne, today: today}
}

func createUnifiedDashboardTask(db *gorm.DB, id int, taskNo string, userID, tokenID uint, modelCode, status string, createdAt time.Time) error {
	var operationID int
	if err := db.Table("gw_model_operations AS operation").Select("operation.id").Joins("JOIN gw_catalog_models catalog ON catalog.id=operation.catalog_model_id AND catalog.release_id=operation.release_id").Joins("JOIN gw_models model ON model.id=catalog.model_id").Where("model.model_code=?", modelCode).Take(&operationID).Error; err != nil {
		return err
	}
	callID := id + 1000
	if err := db.Exec(`INSERT INTO gw_api_calls(id,public_id,user_id,token_id,operation_contract_id,catalog_release_id,model_operation_id,sku_id,status,quoted_amount,created_at,updated_at) VALUES (?,?,?,?,1,1,?,1,?,'0',?,?)`, callID, "call-"+taskNo, userID, tokenID, operationID, status, createdAt, createdAt).Error; err != nil {
		return err
	}
	if err := db.Exec(`INSERT INTO gw_api_resources(id,public_id,resource_kind,call_id,user_id,token_id,created_at) VALUES (?,?, 'capability_task',?,?,?,?)`, id, taskNo, callID, userID, tokenID, createdAt).Error; err != nil {
		return err
	}
	return db.Exec(`INSERT INTO gw_capability_tasks(resource_id,task_no,status,progress,created_at,updated_at) VALUES (?,?,?,100,?,?)`, id, taskNo, status, createdAt, createdAt).Error
}

func dashboardCall(id string, userID, tokenID uint, modelCode string, status model.APICallStatus, cost float64, input, output, total int, createdAt time.Time) model.APICall {
	completedAt := createdAt.Add(time.Second)
	return model.APICall{
		ID: id, RequestID: "request-" + id, UserID: userID, TokenID: tokenID,
		Endpoint: "/v1/test", Operation: "test", Model: modelCode, Status: status,
		InputTokens: input, OutputTokens: output, TotalTokens: total,
		FinalCost: decimal.NewFromFloat(cost), StartedAt: createdAt, CompletedAt: &completedAt, CreatedAt: createdAt,
	}
}

func assertTodayStats(t *testing.T, today map[string]any, total, success, failed int64, cost float64) {
	t.Helper()
	if today["total_requests"] != total || today["success_count"] != success || today["failed_count"] != failed {
		t.Fatalf("today counts=%v", today)
	}
	actualCost, ok := today["total_cost"].(float64)
	if !ok || math.Abs(actualCost-cost) > 0.0001 {
		t.Fatalf("today cost=%v", today["total_cost"])
	}
}

func capabilityDistribution(items []CapabilityDist) map[string]int64 {
	result := make(map[string]int64, len(items))
	for _, item := range items {
		result[item.Capability] = item.Count
	}
	return result
}

func modelRankingMap(items []ModelCallRanking) map[string]ModelCallRanking {
	result := make(map[string]ModelCallRanking, len(items))
	for _, item := range items {
		result[item.ModelCode] = item
	}
	return result
}

func channelRateMap(items []ChannelSuccessRate) map[string]ChannelSuccessRate {
	result := make(map[string]ChannelSuccessRate, len(items))
	for _, item := range items {
		result[channelRateKey(item.RouteKind, item.ChannelID)] = item
	}
	return result
}

func assertChannelRate(t *testing.T, item ChannelSuccessRate, total, success int64, rate float64) {
	t.Helper()
	if item.Total != total || item.Success != success || math.Abs(item.Rate-rate) > 0.0001 {
		t.Fatalf("channel rate=%#v want total=%d success=%d rate=%f", item, total, success, rate)
	}
}
