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
	assertTodayStats(t, userStats.Today, 3, 2, 1, 3.25)
	today := seed.today.Format("01-02")
	var todayTrend *DailyStats
	for index := range userStats.WeeklyTrend {
		if userStats.WeeklyTrend[index].Date == today {
			todayTrend = &userStats.WeeklyTrend[index]
			break
		}
	}
	if todayTrend == nil || todayTrend.Requests != 3 || todayTrend.Errors != 1 || math.Abs(todayTrend.Cost-3.25) > 0.0001 {
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
	assertTodayStats(t, adminStats.Today, 4, 3, 1, 12.25)
	adminDistribution := capabilityDistribution(adminStats.CapabilityDist)
	if adminDistribution["image-model"] != 2 || adminDistribution["video-model"] != 1 {
		t.Fatalf("admin capability distribution=%v", adminDistribution)
	}

	userEnhanced, err := service.GetChatStats(7, seed.userOne, false)
	if err != nil {
		t.Fatal(err)
	}
	if userEnhanced.TokenUsage.TotalPromptTokens != 21 || userEnhanced.TokenUsage.TotalCompletionTokens != 5 || userEnhanced.TokenUsage.TotalTokens != 26 {
		t.Fatalf("user token usage=%#v", userEnhanced.TokenUsage)
	}
	userRankings := modelRankingMap(userEnhanced.ModelRankings)
	if userRankings["shared-model"].Calls != 3 || userRankings["shared-model"].TotalTokens != 22 || userRankings["response-model"].Calls != 1 {
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
	if adminEnhanced.TokenUsage.TotalTokens != 126 {
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
	inserted := model.Task{
		TaskNo: "task-inserted-after-snapshot", UserID: seed.userOne, TokenID: seed.tokenOne,
		ModelCode: "image-model", Status: model.TaskStatusSuccess,
		BaseModel: model.BaseModel{CreatedAt: snapshot.Add(time.Second), UpdatedAt: snapshot.Add(time.Second)},
	}
	if err := model.DB().Create(&inserted).Error; err != nil {
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
	if err := db.AutoMigrate(
		&model.APICall{}, &model.APICallAttempt{}, &model.Channel{}, &model.GwChannel{},
		&model.Model{}, &model.Endpoint{}, &model.Task{},
	); err != nil {
		t.Fatal(err)
	}
	model.SetDB(db)

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	today := todayStart.Add(time.Minute)
	yesterday := todayStart.Add(-time.Hour)
	userOne := uint(11)
	userTwo := uint(22)
	tokenOne := uint(101)

	calls := []model.APICall{
		dashboardCall("call-chat", userOne, tokenOne, "shared-model", model.APICallStatusCompleted, 1, 10, 5, 15, today),
		dashboardCall("call-response", userOne, tokenOne, "response-model", model.APICallStatusFailed, 0.25, 4, 0, 4, today.Add(time.Minute)),
		dashboardCall("call-image", userOne, tokenOne, "shared-model", model.APICallStatusCompleted, 2, 0, 0, 0, today.Add(2*time.Minute)),
		dashboardCall("call-yesterday", userOne, tokenOne, "shared-model", model.APICallStatusCompleted, 4, 7, 0, 7, yesterday),
		dashboardCall("call-other-user", userTwo, 202, "shared-model", model.APICallStatusCompleted, 9, 100, 0, 100, today.Add(3*time.Minute)),
		dashboardCall("call-outside-range", userOne, tokenOne, "old-model", model.APICallStatusCompleted, 20, 1000, 0, 1000, today.AddDate(0, 0, -10)),
	}
	if err := db.Create(&calls).Error; err != nil {
		t.Fatal(err)
	}

	channels := []model.GwChannel{
		{ID: 10, Name: "gateway-a", Protocol: model.ProtocolOpenAI, BaseURL: "https://a.test", Status: 1},
		{ID: 20, Name: "gateway-b", Protocol: model.ProtocolAnthropic, BaseURL: "https://b.test", Status: 1},
	}
	if err := db.Create(&channels).Error; err != nil {
		t.Fatal(err)
	}
	capabilityChannel := model.Channel{BaseModel: model.BaseModel{ID: 30}, Type: "image", Name: "image", Status: 1}
	if err := db.Create(&capabilityChannel).Error; err != nil {
		t.Fatal(err)
	}
	capabilityEndpoint := model.Endpoint{
		BaseModel: model.BaseModel{ID: 300}, ModelCode: "image-model", ChannelID: capabilityChannel.ID,
		Protocol: model.ProtocolOpenAI, RequestPath: "/images", RequestMethod: "POST",
		VendorModel: "image-vendor", InteractionMode: model.ModeSync, Status: 1,
	}
	if err := db.Create(&capabilityEndpoint).Error; err != nil {
		t.Fatal(err)
	}

	capabilityAttempt := dashboardAttempt("call-image", 1, model.APICallRouteCapability, 0, model.APICallAttemptStatusCompleted, today.Add(2*time.Minute))
	capabilityAttempt.EndpointID = capabilityEndpoint.ID
	attempts := []model.APICallAttempt{
		dashboardAttempt("call-chat", 1, model.APICallRouteGatewayV2, 10, model.APICallAttemptStatusFailed, today),
		dashboardAttempt("call-chat", 2, model.APICallRouteGatewayV2, 10, model.APICallAttemptStatusCompleted, today.Add(time.Second)),
		dashboardAttempt("call-chat", 3, model.APICallRouteGatewayV2, 10, model.APICallAttemptStatusStarted, today.Add(2*time.Second)),
		dashboardAttempt("call-response", 1, model.APICallRouteGatewayV2, 20, model.APICallAttemptStatusFailed, today.Add(time.Minute)),
		capabilityAttempt,
		dashboardAttempt("call-other-user", 1, model.APICallRouteGatewayV2, 10, model.APICallAttemptStatusCompleted, today.Add(3*time.Minute)),
		dashboardAttempt("call-outside-range", 1, model.APICallRouteGatewayV2, 10, model.APICallAttemptStatusCompleted, today.AddDate(0, 0, -10)),
	}
	if err := db.Create(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.APICallAttempt{}).
		Where("call_id = ?", "call-response").
		UpdateColumn("route_kind", "").Error; err != nil {
		t.Fatal(err)
	}

	tasks := []model.Task{
		{BaseModel: model.BaseModel{CreatedAt: today}, TaskNo: "task-user-image", UserID: userOne, TokenID: tokenOne, ModelCode: "image-model", ChannelID: 30, Status: model.TaskStatusSuccess},
		{BaseModel: model.BaseModel{CreatedAt: today.Add(time.Minute)}, TaskNo: "task-user-video", UserID: userOne, TokenID: tokenOne, ModelCode: "video-model", Status: model.TaskStatusFailed},
		{BaseModel: model.BaseModel{CreatedAt: today.Add(2 * time.Minute)}, TaskNo: "task-admin-image", UserID: userTwo, TokenID: 202, ModelCode: "image-model", ChannelID: 30, Status: model.TaskStatusSuccess},
	}
	if err := db.Create(&tasks).Error; err != nil {
		t.Fatal(err)
	}
	return dashboardSeed{userOne: userOne, tokenOne: tokenOne, today: today}
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

func dashboardAttempt(callID string, attemptNo int, routeKind string, channelID uint, status model.APICallAttemptStatus, startedAt time.Time) model.APICallAttempt {
	completedAt := startedAt.Add(time.Second)
	return model.APICallAttempt{
		CallID: callID, AttemptNo: attemptNo, RouteKind: routeKind, ChannelID: channelID,
		Status: status, StartedAt: startedAt, CompletedAt: &completedAt, CreatedAt: startedAt,
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
