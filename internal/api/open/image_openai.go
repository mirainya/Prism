package open

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/service"
)

// OpenAIImageRequest OpenAI 标准图像生成请求
// 参考 https://platform.openai.com/docs/api-reference/images/create
type OpenAIImageRequest struct {
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	N                 int    `json:"n"`
	Size              string `json:"size"`
	Quality           string `json:"quality"`
	ResponseFormat    string `json:"response_format"` // url | b64_json
	OutputFormat      string `json:"output_format"`
	OutputCompression *int   `json:"output_compression"`
	Moderation        string `json:"moderation"`
	Style             string `json:"style"`
	User              string `json:"user"`
}

// OpenAIImageData 单张图片结果
type OpenAIImageData struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

// OpenAIImageResponse OpenAI 标准图像响应
type OpenAIImageResponse struct {
	Created int64             `json:"created"`
	Data    []OpenAIImageData `json:"data"`
}

// openAIError 返回 OpenAI 风格的错误(不套 Prism {code,data} 外壳)
func openAIError(c *gin.Context, httpCode int, message, errType string) {
	c.JSON(httpCode, gin.H{
		"error": gin.H{
			"message": message,
			"type":    errType,
		},
	})
}

// CreateImageGenerationOpenAI POST /v1/images/generations
// 真正的 OpenAI 标准协议:同步返图,网关自动适配同步/异步渠道
func CreateImageGenerationOpenAI(c *gin.Context) {
	var req OpenAIImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		openAIError(c, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error")
		return
	}
	if req.Model == "" || req.Prompt == "" {
		openAIError(c, http.StatusBadRequest, "model and prompt are required", "invalid_request_error")
		return
	}

	token := middleware.GetToken(c)
	if token == nil {
		openAIError(c, http.StatusUnauthorized, "unauthorized", "authentication_error")
		return
	}

	// 组装 params: prompt 之外的 OpenAI 字段透传给渠道映射层
	params := map[string]any{"prompt": req.Prompt}
	if req.N > 0 {
		params["n"] = req.N
	}
	if req.Size != "" {
		params["size"] = req.Size
	}
	if req.Quality != "" {
		params["quality"] = req.Quality
	}
	if req.OutputFormat != "" {
		params["output_format"] = req.OutputFormat
	}
	if req.OutputCompression != nil {
		params["output_compression"] = *req.OutputCompression
	}
	if req.Moderation != "" {
		params["moderation"] = req.Moderation
	}
	if req.Style != "" {
		params["style"] = req.Style
	}

	result, err := capabilityService.InvokeAndWait(c.Request.Context(), &service.InvokeRequest{
		UserID:     token.UserID,
		TokenID:    token.ID,
		Capability: "text2img",
		Model:      req.Model,
		Params:     params,
	}, 0)
	if err != nil {
		openAIError(c, http.StatusInternalServerError, err.Error(), "api_error")
		return
	}

	// 明确失败
	if result.Done && !result.Success {
		openAIError(c, http.StatusBadGateway, result.Error, "api_error")
		return
	}

	// 超时降级: 未拿到最终结果,返回 202 + task_id 供客户端后续查询
	if !result.Done {
		c.JSON(http.StatusAccepted, gin.H{
			"status":  "processing",
			"task_id": result.TaskNo,
			"message": "image generation still in progress, query via GET /v1/tasks/{task_id}",
		})
		return
	}

	// 成功: 组装标准 OpenAI 响应
	data := make([]OpenAIImageData, 0, len(result.URLs))
	for _, u := range result.URLs {
		data = append(data, OpenAIImageData{
			URL:           u,
			RevisedPrompt: result.RevisedPrompt,
		})
	}

	c.JSON(http.StatusOK, OpenAIImageResponse{
		Created: time.Now().Unix(),
		Data:    data,
	})
}
