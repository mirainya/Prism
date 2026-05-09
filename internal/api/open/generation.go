package open

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/model"
	"github.com/mirainya/Prism/internal/provider"
	"github.com/mirainya/Prism/internal/service"
	"github.com/mirainya/Prism/internal/worker"
	"github.com/mirainya/Prism/pkg/errors"
	"github.com/shopspring/decimal"
)

type GenerationRequest struct {
	Model       string         `json:"model" binding:"required"`
	Prompt      string         `json:"prompt" binding:"required"`
	Params      map[string]any `json:"params"`
	CallbackURL string         `json:"callback_url"`
}

type GenerationResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
}

var (
	strategyService = service.NewStrategyService()
	taskService     = service.NewTaskService()
	billingService  = service.NewBillingService()
)

func CreateImageGeneration(c *gin.Context) {
	createGeneration(c, model.CapabilityText2Img)
}

func CreateVideoGeneration(c *gin.Context) {
	createGeneration(c, model.CapabilityText2Video)
}

func createGeneration(c *gin.Context, capabilityCode string) {
	var req GenerationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrInvalidParams, err.Error()))
		return
	}

	token := middleware.GetToken(c)
	if token == nil {
		resp.ErrorMsg(c, http.StatusUnauthorized, 401, "unauthorized")
		return
	}
	tokenID := token.ID
	userID := token.UserID

	// 1. 选择渠道能力配置
	ccResult, err := strategyService.SelectChannelCapability(req.Model)
	if err != nil {
		resp.BadRequest(c, errors.ErrNoAvailableChannel)
		return
	}

	// 2. 选择账号
	accountResult, err := strategyService.SelectAccount(ccResult.Channel.ID)
	if err != nil {
		resp.BadRequest(c, errors.WithMessage(errors.ErrNoAvailableChannel, "no available account"))
		return
	}

	// 3. 参数转换
	paramTemplate, _ := provider.ParseParamTemplate(ccResult.ChannelCapability.ParamMapping)
	converter := provider.NewDefaultConverter()

	params := map[string]any{
		"prompt": req.Prompt,
	}
	for k, v := range req.Params {
		params[k] = v
	}

	mappedParams, err := converter.Convert(params, paramTemplate)
	if err != nil {
		resp.InternalError(c, errors.WithMessage(errors.ErrProviderError, "param convert error"))
		return
	}

	// 4. 预扣费
	price := ccResult.ChannelCapability.Price
	if price.GreaterThan(decimal.Zero) {
		if err := billingService.Deduct(tokenID, userID, price); err != nil {
			resp.BadRequest(c, errors.WithMessage(errors.ErrInsufficientQuota, err.Error()))
			return
		}
	}

	// 5. 创建任务
	task, err := taskService.CreateTask(&service.CreateTaskRequest{
		UserID:              userID,
		TokenID:             tokenID,
		CapabilityCode:      capabilityCode,
		ChannelID:           ccResult.Channel.ID,
		ChannelCapabilityID: ccResult.ChannelCapability.ID,
		AccountID:           accountResult.Account.ID,
		RequestParams:       params,
		MappedParams:        mappedParams,
		CallbackURL:         req.CallbackURL,
		Cost:                price,
	})
	if err != nil {
		if price.GreaterThan(decimal.Zero) {
			_ = billingService.Refund(tokenID, userID, price)
		}
		resp.InternalError(c, errors.WithMessage(errors.ErrInternalError, "create task error"))
		return
	}

	// 6. 入队异步任务
	if err := worker.EnqueueTaskSubmit(task.ID); err != nil {
		taskService.UpdateTaskFail(task.ID, "enqueue task error")
		resp.InternalError(c, errors.WithMessage(errors.ErrInternalError, "enqueue task error"))
		return
	}

	resp.Success(c, GenerationResponse{
		ID:        task.TaskNo,
		Status:    string(task.Status),
		CreatedAt: task.CreatedAt.Format("2006-01-02T15:04:05Z"),
	})
}
