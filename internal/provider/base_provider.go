package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mirainya/Prism/pkg/config"
)

// 全局 http.Client，复用 TCP 连接池
var sharedHTTPClient *http.Client

// InitHTTPClient 根据配置初始化全局 HTTP Client，应在 main 中调用
func InitHTTPClient() {
	cfg := config.C.HTTPClient

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30
	}
	maxIdleConns := cfg.MaxIdleConns
	if maxIdleConns <= 0 {
		maxIdleConns = 100
	}
	maxIdleConnsPerHost := cfg.MaxIdleConnsPerHost
	if maxIdleConnsPerHost <= 0 {
		maxIdleConnsPerHost = 20
	}
	idleConnTimeout := cfg.IdleConnTimeout
	if idleConnTimeout <= 0 {
		idleConnTimeout = 90
	}

	sharedHTTPClient = &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        maxIdleConns,
			MaxIdleConnsPerHost: maxIdleConnsPerHost,
			IdleConnTimeout:     time.Duration(idleConnTimeout) * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		},
	}
}

type BaseProvider struct {
	Name            string
	BaseURL         string
	APIKey          string
	AuthLocation    string // header/body/query
	AuthKey         string
	AuthValuePrefix string
	ContentType     string // application/json / application/x-www-form-urlencoded / multipart/form-data
	RequestMethod   string // POST / GET
	SubmitPath      string
	ProgressPath    string
	Converter       ParamConverter
	Parser          ResponseParser
	ResponseMapping *ResponseMapping
	CallbackMapping *ResponseMapping
}

// resolvePath 替换路径中的 {variable} 模板变量
// 例如 /v1/models/{model}/generate + params["model"]="sd-xl" → /v1/models/sd-xl/generate
func resolvePath(path string, params map[string]any) string {
	for k, v := range params {
		placeholder := "{" + k + "}"
		if strings.Contains(path, placeholder) {
			path = strings.ReplaceAll(path, placeholder, fmt.Sprint(v))
		}
	}
	return path
}

// buildRequestBody 根据 ContentType 构建请求体
func (p *BaseProvider) buildRequestBody(params map[string]any) (io.Reader, string) {
	ct := p.ContentType
	if ct == "application/x-www-form-urlencoded" {
		form := url.Values{}
		for k, v := range params {
			form.Set(k, fmt.Sprintf("%v", v))
		}
		return strings.NewReader(form.Encode()), ct
	}
	if ct == "multipart/form-data" {
		return p.buildMultipartBody(params)
	}
	// 默认 JSON
	bodyBytes, _ := json.Marshal(params)
	return bytes.NewReader(bodyBytes), "application/json"
}

// buildMultipartBody 构建 multipart 请求体，支持文件上传
// 文件字段约定：值以 "@base64:" 前缀表示 base64 编码的文件数据
// 格式：@base64:filename.png:iVBORw0KGgo...
func (p *BaseProvider) buildMultipartBody(params map[string]any) (io.Reader, string) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for k, v := range params {
		strVal := fmt.Sprintf("%v", v)

		if strings.HasPrefix(strVal, "@base64:") {
			// 解析格式：@base64:filename:base64data
			rest := strVal[len("@base64:"):]
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) == 2 {
				filename := parts[0]
				data, err := base64.StdEncoding.DecodeString(parts[1])
				if err == nil {
					part, err := writer.CreateFormFile(k, filename)
					if err == nil {
						part.Write(data)
						continue
					}
				}
			}
		}

		// 普通字段
		writer.WriteField(k, strVal)
	}

	writer.Close()
	return &buf, writer.FormDataContentType()
}

// setAuth 设置认证信息
func (p *BaseProvider) setAuth(httpReq *http.Request) {
	if p.AuthLocation == "" || p.AuthLocation == "header" {
		httpReq.Header.Set(p.AuthKey, p.AuthValuePrefix+p.APIKey)
	}
}

// appendQueryAuth 给 URL 追加 query 认证参数
func (p *BaseProvider) appendQueryAuth(rawURL string) string {
	if p.AuthLocation != "query" {
		return rawURL
	}
	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}
	return rawURL + sep + url.QueryEscape(p.AuthKey) + "=" + url.QueryEscape(p.APIKey)
}

func (p *BaseProvider) Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error) {
	// 路径模板变量替换
	submitPath := resolvePath(p.SubmitPath, req.Params)
	reqURL := p.appendQueryAuth(p.BaseURL + submitPath)

	// body 认证：将 token 注入到请求参数中
	params := req.Params
	if p.AuthLocation == "body" {
		if params == nil {
			params = make(map[string]any)
		}
		params[p.AuthKey] = p.AuthValuePrefix + p.APIKey
	}

	body, contentType := p.buildRequestBody(params)

	method := p.RequestMethod
	if method == "" {
		method = "POST"
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", contentType)
	p.setAuth(httpReq)

	resp, err := sharedHTTPClient.Do(httpReq)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return SubmitResult{}, fmt.Errorf("api error: %s", string(respBody))
	}

	return p.Parser.ParseSubmitResponse(respBody, p.ResponseMapping)
}

func (p *BaseProvider) GetProgress(ctx context.Context, providerTaskID string) (ProgressResult, error) {
	// 路径模板变量替换
	progressPath := resolvePath(p.ProgressPath, map[string]any{"task_id": providerTaskID})
	reqURL := p.appendQueryAuth(fmt.Sprintf("%s%s/%s", p.BaseURL, progressPath, providerTaskID))

	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return ProgressResult{}, fmt.Errorf("create request: %w", err)
	}

	p.setAuth(httpReq)

	resp, err := sharedHTTPClient.Do(httpReq)
	if err != nil {
		return ProgressResult{}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ProgressResult{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return ProgressResult{Error: string(respBody)}, nil
	}

	return p.Parser.ParseProgressResponse(respBody, p.ResponseMapping)
}

// ParseCallback 使用独立的 CallbackMapping 解析回调
func (p *BaseProvider) ParseCallback(ctx context.Context, body []byte) (ProgressResult, string, error) {
	// 优先使用 CallbackMapping，如果没有配置则回退到 ResponseMapping
	mapping := p.CallbackMapping
	if mapping == nil {
		mapping = p.ResponseMapping
	}

	return p.Parser.ParseCallbackResponse(body, mapping)
}
