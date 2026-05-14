package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// 纯配置驱动的 Chat Completion PoC
// 证明：只需要 base_url + request_path + auth + vendor_model，不需要任何 provider-specific 代码

type Config struct {
	BaseURL     string
	RequestPath string
	APIKey      string
	AuthPrefix  string // "Bearer "
	VendorModel string
}

func main() {
	// 从数据库读出的配置（linkapi channel + gemini-3-pro-preview）
	cfg := Config{
		BaseURL:     "https://api.linkapi.org",
		RequestPath: "/v1/chat/completions",
		APIKey:      "sk-ybZV1HjYRjwwtls0B0u6Lkk69jWlmWR97Y3NGyvIHgQsKfRk",
		AuthPrefix:  "Bearer ",
		VendorModel: "gemini-3-pro-preview",
	}

	// 构造标准 OpenAI 格式请求体
	body := map[string]any{
		"model": cfg.VendorModel,
		"messages": []map[string]string{
			{"role": "user", "content": "说一个字"},
		},
		"stream":     true,
		"max_tokens": 100,
	}

	data, _ := json.Marshal(body)
	url := cfg.BaseURL + cfg.RequestPath

	req, _ := http.NewRequest("POST", url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", cfg.AuthPrefix+cfg.APIKey)

	fmt.Printf("=== 配置驱动 Chat PoC ===\n")
	fmt.Printf("URL: %s\n", url)
	fmt.Printf("Model: %s\n", cfg.VendorModel)
	fmt.Printf("Stream: true\n\n")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	fmt.Printf("Status: %d\n\n", resp.StatusCode)

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Printf("Error: %s\n", string(b))
		return
	}

	// 流式读取 SSE
	fmt.Printf("=== 流式响应 ===\n")
	scanner := bufio.NewScanner(resp.Body)
	var fullContent strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) == nil && len(chunk.Choices) > 0 {
			content := chunk.Choices[0].Delta.Content
			fullContent.WriteString(content)
			fmt.Print(content)
		}
	}
	fmt.Printf("\n\n=== 完整输出 ===\n%s\n", fullContent.String())
	fmt.Printf("\n✓ 纯配置驱动，零 provider 代码，流式 Chat 成功！\n")
}
