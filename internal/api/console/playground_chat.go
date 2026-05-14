package console

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mirainya/Prism/internal/api/resp"
	"github.com/mirainya/Prism/internal/provider/chat"
	"github.com/mirainya/Prism/internal/service"
)

// PlaygroundChatCompletions POST /api/playground/:token_id/chat/completions
func PlaygroundChatCompletions(c *gin.Context) {
	token, ok := getPlaygroundToken(c)
	if !ok {
		return
	}

	var req struct {
		Model            string                `json:"model" binding:"required"`
		Messages         []chat.ChatMessage    `json:"messages" binding:"required,min=1"`
		Temperature      float64               `json:"temperature"`
		MaxTokens        int                   `json:"max_tokens"`
		TopP             float64               `json:"top_p"`
		FrequencyPenalty float64               `json:"frequency_penalty"`
		PresencePenalty  float64               `json:"presence_penalty"`
		Stop             []string              `json:"stop"`
		Stream           *bool                 `json:"stream"`
		Tools            []chat.ToolDefinition `json:"tools"`
		ToolChoice       any                   `json:"tool_choice"`
		ResponseFormat   *chat.ResponseFormat  `json:"response_format"`
		Seed             *int                  `json:"seed"`
		User             string                `json:"user"`
		ConversationID   string                `json:"conversation_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		resp.ErrorMsg(c, http.StatusBadRequest, 400, err.Error())
		return
	}

	streamValue := false
	streamSpecified := false
	if req.Stream != nil {
		streamValue = *req.Stream
		streamSpecified = true
	}

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
		Stream:           streamValue,
		StreamSpecified:  streamSpecified,
		Tools:            req.Tools,
		ToolChoice:       req.ToolChoice,
		ResponseFormat:   req.ResponseFormat,
		Seed:             req.Seed,
		User:             req.User,
		ConversationID:   req.ConversationID,
	}

	if completionReq.StreamSpecified && completionReq.Stream {
		session, err := chatService.StreamComplete(c.Request.Context(), completionReq)
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

		aggregation, streamErr := proxyPlaygroundStream(c, session.UpstreamResp)
		_, finalizeErr := chatService.FinalizeStream(session, aggregation, streamErr)
		if finalizeErr != nil {
			resp.ErrorMsg(c, http.StatusInternalServerError, 500, finalizeErr.Error())
			return
		}
		if streamErr != nil {
			return
		}
		return
	}

	chatResp, err := chatService.Complete(c.Request.Context(), completionReq)
	if err != nil {
		resp.ErrorMsg(c, http.StatusInternalServerError, 500, err.Error())
		return
	}

	c.JSON(http.StatusOK, chatResp)
}

func proxyPlaygroundStream(c *gin.Context, upstreamResp *http.Response) (*service.StreamAggregationResult, error) {
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
					mergeSSEChunk(aggregation, payload)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				return aggregation, nil
			}
			aggregation.ErrorMessage = err.Error()
			return aggregation, err
		}
	}
}

func mergeSSEChunk(aggregation *service.StreamAggregationResult, payload string) {
	var parsed struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *chat.ChatUsage `json:"usage"`
		Error any             `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return
	}
	if len(parsed.Choices) > 0 {
		choice := parsed.Choices[0]
		aggregation.AssistantContent += choice.Delta.Content
		aggregation.ReasoningContent += choice.Delta.ReasoningContent
		if choice.FinishReason != "" {
			aggregation.FinishReason = choice.FinishReason
		}
	}
	if parsed.Usage != nil {
		aggregation.Usage = parsed.Usage
	}
	if parsed.Error != nil {
		aggregation.ErrorMessage = fmt.Sprint(parsed.Error)
	}
	aggregation.ResponsePreview = truncateForStreamPreview(aggregation.AssistantContent)
	aggregation.ResponseBody = payload
}

func truncateForStreamPreview(value string) string {
	runes := []rune(value)
	if len(runes) <= 500 {
		return value
	}
	return string(runes[:500]) + "..."
}
