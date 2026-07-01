package open

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/middleware"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

// ChatCompletions POST /v1/chat/completions
func ChatCompletions(c *gin.Context) {
	var req struct {
		Model            string                `json:"model" binding:"required"`
		Messages         []chat.ChatMessage    `json:"messages" binding:"required,min=1"`
		Temperature      *float64              `json:"temperature"`
		MaxTokens        int                   `json:"max_tokens"`
		TopP             *float64              `json:"top_p"`
		FrequencyPenalty *float64              `json:"frequency_penalty"`
		PresencePenalty  *float64              `json:"presence_penalty"`
		Stop             []string              `json:"stop"`
		Stream           bool                  `json:"stream"`
		Tools            []chat.ToolDefinition `json:"tools"`
		ToolChoice       any                   `json:"tool_choice"`
		ResponseFormat   *chat.ResponseFormat  `json:"response_format"`
		Seed             *int                  `json:"seed"`
		User             string                `json:"user"`
		ConversationID   string                `json:"conversation_id"`
		ReasoningEffort  *string               `json:"reasoning_effort"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	token := middleware.GetToken(c)
	completionReq := &service.CompletionRequest{
		UserID:           token.UserID,
		TokenID:          token.ID,
		Model:            req.Model,
		Messages:         req.Messages,
		Temperature:      req.Temperature,
		MaxTokens:        req.MaxTokens,
		TopP:             req.TopP,
		FrequencyPenalty: req.FrequencyPenalty,
		PresencePenalty:  req.PresencePenalty,
		Stop:             req.Stop,
		Stream:           req.Stream,
		StreamSpecified:  true,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
		ConversationID:   req.ConversationID,
		ReasoningEffort:  req.ReasoningEffort,
	}

	svc := service.NewUnifiedService()

	if req.Stream {
		session, err := svc.StreamComplete(c.Request.Context(), completionReq)
		if err != nil {
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
			return
		}
		defer session.CleanupFunc()
		defer session.UpstreamResp.Body.Close()

		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
		c.Status(http.StatusOK)

		aggregation, streamErr := proxyOpenStream(c, session.UpstreamResp)
		svc.FinalizeStream(session, aggregation, streamErr)
		return
	}

	chatResp, err := svc.Complete(c.Request.Context(), completionReq)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	c.JSON(http.StatusOK, chatResp)
}

// GetChatModelDetail GET /v1/models/:code
func GetChatModelDetail(c *gin.Context) {
	svc := service.NewUnifiedService()
	m, err := svc.GetModelDetail(c.Request.Context(), c.Param("code"))
	if err != nil {
		resp.ErrorMsg(c, http.StatusNotFound, 404, "model not found")
		return
	}

	item := gin.H{
		"id":       m.Code,
		"object":   "model",
		"created":  m.CreatedAt.Unix(),
		"owned_by": m.Provider,
	}
	if m.Name != "" {
		item["name"] = m.Name
	}
	if m.Description != "" {
		item["description"] = m.Description
	}
	if m.MaxTokens > 0 {
		item["max_tokens"] = m.MaxTokens
	}
	if len(m.Features) > 0 {
		var features []string
		if json.Unmarshal(m.Features, &features) == nil && len(features) > 0 {
			item["features"] = features
		}
	}
	if len(m.ParamSchema) > 0 {
		var schema any
		if json.Unmarshal(m.ParamSchema, &schema) == nil && schema != nil {
			item["param_schema"] = schema
		}
	}

	c.JSON(http.StatusOK, item)
}

// ListChatModelsPublic GET /v1/models
func ListChatModelsPublic(c *gin.Context) {
	svc := service.NewUnifiedService()
	models, err := svc.ListModels(c.Request.Context())
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	data := make([]gin.H, 0, len(models))
	for _, m := range models {
		item := gin.H{
			"id":       m.Code,
			"object":   "model",
			"created":  m.CreatedAt.Unix(),
			"owned_by": m.Provider,
			"type":     m.Type,
		}
		if m.MaxTokens > 0 {
			item["max_tokens"] = m.MaxTokens
		}
		if len(m.Features) > 0 {
			var features []string
			if json.Unmarshal(m.Features, &features) == nil && len(features) > 0 {
				item["features"] = features
			}
		}
		data = append(data, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   data,
	})
}

func proxyOpenStream(c *gin.Context, upstreamResp *http.Response) (*service.StreamAggregationResult, error) {
	aggregation := &service.StreamAggregationResult{}
	reader := bufio.NewReader(upstreamResp.Body)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			_, _ = c.Writer.Write([]byte(line))
			c.Writer.Flush()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "data: ") {
				payload := strings.TrimPrefix(trimmed, "data: ")
				if payload != "[DONE]" {
					mergeOpenSSEChunk(aggregation, payload)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				aggregation.ErrorMessage = err.Error()
				return aggregation, err
			}
			return aggregation, nil
		}
	}
}

func mergeOpenSSEChunk(aggregation *service.StreamAggregationResult, payload string) {
	var parsed struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage              *chat.ChatUsage `json:"usage"`
		ProviderResponseID string          `json:"provider_response_id"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return
	}
	if parsed.ProviderResponseID != "" {
		aggregation.ProviderResponseID = parsed.ProviderResponseID
	}
	if len(parsed.Choices) > 0 {
		choice := parsed.Choices[0]
		aggregation.AssistantContent += choice.Delta.Content
		aggregation.ReasoningContent += choice.Delta.ReasoningContent
		for _, tc := range choice.Delta.ToolCalls {
			aggregation.AssistantContent += tc.Function.Arguments
		}
		if choice.FinishReason != "" {
			aggregation.FinishReason = choice.FinishReason
		}
	}
	if parsed.Usage != nil {
		aggregation.Usage = parsed.Usage
	}
	runes := []rune(aggregation.AssistantContent)
	if len(runes) <= 500 {
		aggregation.ResponsePreview = aggregation.AssistantContent
	} else {
		aggregation.ResponsePreview = string(runes[:500]) + "..."
	}
	aggregation.ResponseBody = fmt.Sprintf("{\"content\":\"%s\"}", payload)
}
