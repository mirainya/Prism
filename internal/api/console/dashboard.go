package console

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/gateway/payloadview"
	"github.com/mirainya/Prism/internal/gateway/repository"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/service"
)

var dashboardService = service.NewDashboardService()

// DashboardStats 仪表盘统计数据
func DashboardStats(c *gin.Context) {
	userID := middleware.GetUserID(c)
	role := middleware.GetUserRole(c)
	isAdmin := role == string(model.UserRoleAdmin)

	result, err := dashboardService.GetStats(userID, isAdmin)
	if err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to get stats")
		return
	}

	resp.Success(c, gin.H{
		"today":           result.Today,
		"weekly_trend":    result.WeeklyTrend,
		"capability_dist": result.CapabilityDist,
	})
}

// ListTasks 任务列表
func ListTasks(c *gin.Context) {
	userID := middleware.GetUserID(c)
	role := middleware.GetUserRole(c)
	isAdmin := role == string(model.UserRoleAdmin)

	var req service.ListTasksRequest
	c.ShouldBindQuery(&req)

	result, err := dashboardService.ListTasks(&req, userID, isAdmin)
	if err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to list tasks")
		return
	}

	resp.Success(c, gin.H{
		"items":       result.Items,
		"total":       result.Total,
		"page":        result.Page,
		"page_size":   result.PageSize,
		"snapshot_at": result.SnapshotAt,
	})
}

// GetTaskDetail 获取任务详情
func GetTaskDetail(c *gin.Context) {
	userID := middleware.GetUserID(c)
	role := middleware.GetUserRole(c)
	isAdmin := role == string(model.UserRoleAdmin)

	taskNo := c.Param("task_no")

	task, err := dashboardService.GetTaskDetail(taskNo, userID, isAdmin)
	if err != nil {
		resp.ErrorMsg(c, 404, 404, "task not found")
		return
	}

	db, err := model.DB().DB()
	if err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to load task detail")
		return
	}
	var gatewayCallID uint64
	var requestPayloadID, resultPayloadID sql.NullInt64
	if err := db.QueryRowContext(c.Request.Context(), `SELECT id,request_payload_id,result_payload_id FROM gw_api_calls WHERE public_id=?`, task.CallID).Scan(&gatewayCallID, &requestPayloadID, &resultPayloadID); err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to load task detail")
		return
	}
	store, err := repository.New(db)
	if err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to load task detail")
		return
	}
	rawParams, requestExpired, err := readConsoleCallPayload(c, store, gatewayCallID, requestPayloadID, "request")
	if err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to load task detail")
		return
	}
	result, resultExpired, err := readConsoleCallPayload(c, store, gatewayCallID, resultPayloadID, "result")
	if err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to load task detail")
		return
	}
	errorMessage := task.ErrorMessage
	if errorMessage == "" && task.Status == model.TaskStatusFailed {
		failure, readErr := payloadview.ReadLatestRequestFailure(c.Request.Context(), store, gatewayCallID)
		if readErr != nil {
			resp.ErrorMsg(c, 500, 500, "failed to load task detail")
			return
		}
		errorMessage = failure.Message
		if errorMessage == "" {
			errorMessage = failure.Code
		}
	}

	detail := gin.H{
		"task_no":                 task.TaskNo,
		"call_id":                 task.CallID,
		"capability":              task.ModelCode,
		"status":                  task.Status.Public(),
		"progress":                task.Progress,
		"cost":                    task.Cost,
		"refunded":                task.Refunded,
		"callback_status":         task.CallbackStatus,
		"callback_attempts":       task.CallbackAttempts,
		"error":                   errorMessage,
		"result":                  result,
		"raw_params":              rawParams,
		"request_payload_expired": requestExpired,
		"result_payload_expired":  resultExpired,
		"created_at":              task.CreatedAt.Format("2006-01-02 15:04:05"),
	}

	if isAdmin {
		detail["gateway_call_id"] = gatewayCallID
		detail["vendor_task_id"] = task.VendorTaskID
		if task.Channel != nil {
			detail["channel"] = task.Channel.Type
		}
	}
	if task.Endpoint != nil && task.Endpoint.Model != nil {
		detail["capability_name"] = task.Endpoint.Model.Name
	}
	if task.StartedAt != nil {
		detail["started_at"] = task.StartedAt.Format("2006-01-02 15:04:05")
	}
	if task.CompletedAt != nil {
		detail["completed_at"] = task.CompletedAt.Format("2006-01-02 15:04:05")
	}

	resp.Success(c, detail)
}

func readConsoleCallPayload(c *gin.Context, store *repository.Store, callID uint64, payloadID sql.NullInt64, kind string) (any, bool, error) {
	if !payloadID.Valid || payloadID.Int64 <= 0 {
		return nil, false, nil
	}
	plain, err := payloadview.ReadCallPayload(c.Request.Context(), store, callID, uint64(payloadID.Int64), kind)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer clear(plain)
	var value any
	if err := json.Unmarshal(plain, &value); err != nil {
		return nil, false, err
	}
	return value, false, nil
}

// ChatStats Chat 增强统计
func ChatStats(c *gin.Context) {
	days := 7
	if d, err := strconv.Atoi(c.DefaultQuery("days", "7")); err == nil && d > 0 && d <= 90 {
		days = d
	}
	userID := middleware.GetUserID(c)
	isAdmin := middleware.GetUserRole(c) == string(model.UserRoleAdmin)
	result, err := dashboardService.GetChatStats(days, userID, isAdmin)
	if err != nil {
		resp.ErrorMsg(c, 500, 500, "failed to get chat stats")
		return
	}
	resp.Success(c, result)
}
