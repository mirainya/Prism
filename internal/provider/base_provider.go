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

	"github.com/mirainya/Prism/internal/domain"
	"github.com/mirainya/Prism/pkg/config"
	"github.com/tidwall/gjson"
)

// upstreamErrorPaths 常见上游错误消息字段路径,按优先级探测
var upstreamErrorPaths = []string{"error.message", "error.msg", "message", "msg", "error", "detail"}

// extractUpstreamErrorMessage 从上游错误响应体中提取人类可读的错误消息
// 按优先级探测常见字段,取不到则回退原始 body(截断避免过长)
func extractUpstreamErrorMessage(body []byte) string {
	s := string(body)
	if gjson.Valid(s) {
		for _, path := range upstreamErrorPaths {
			if v := gjson.Get(s, path); v.Exists() {
				if msg := strings.TrimSpace(v.String()); msg != "" {
					return msg
				}
			}
		}
	}
	// 回退: 原始 body,截断到 512 字符避免超长
	s = strings.TrimSpace(s)
	if len(s) > 512 {
		return s[:512]
	}
	return s
}

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
	Name                string
	BaseURL             string
	APIKey              string
	AuthLocation        string // header/body/query
	AuthKey             string
	AuthValuePrefix     string
	ContentType         string // application/json / application/x-www-form-urlencoded / multipart/form-data
	RequestMethod       string // POST / GET
	PollMethod          string // GET / POST
	SubmitPath          string
	ProgressPath        string
	Parser              ResponseParser
	ResponseMapping     *ResponseMapping
	PollResponseMapping *ResponseMapping
	CallbackMapping     *ResponseMapping
	Timeout             int  // endpoint 级请求超时(秒),0 用全局
	Streaming           bool // 交互模式=stream: 自动注入 stream:true 并按 SSE 解析响应

	// 图生图配置(端点 extra_config.image_edit,nil=不支持图生图)
	// 请求带非空 ImageEditField 字段时:切 ImageEditPath 路径 + 强制 multipart 文件上传。
	// 豆包/duomi 等 JSON 直传 URL 的渠道不配此项,走原路径不受影响。
	ImageEditPath  string // 图生图请求路径,如 /v1/images/edits;空=不启用
	ImageEditField string // 参考图字段名,如 image
}

// hasImageEditInput 判断请求是否携带参考图(图生图):
// 端点配了 ImageEditPath/Field,且 params 里对应字段有非空值(字符串或非空数组)。
func (p *BaseProvider) hasImageEditInput(params map[string]any) bool {
	if p.ImageEditPath == "" || p.ImageEditField == "" {
		return false
	}
	v, ok := params[p.ImageEditField]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val) != ""
	case []any:
		return len(val) > 0
	case []string:
		return len(val) > 0
	default:
		return v != nil
	}
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

// buildRequestBody 根据 contentType 构建请求体
func (p *BaseProvider) buildRequestBody(params map[string]any, contentType string) (io.Reader, string) {
	ct := contentType
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
// 值为数组时(多图)：逐个以同名字段写多份文件(如 image),兼容 OpenAI edits 多参考图。
func (p *BaseProvider) buildMultipartBody(params map[string]any) (io.Reader, string) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	writeFileOrField := func(key, strVal string) {
		if strings.HasPrefix(strVal, "@base64:") {
			// 解析格式：@base64:filename:base64data
			rest := strVal[len("@base64:"):]
			parts := strings.SplitN(rest, ":", 2)
			if len(parts) == 2 {
				if data, err := base64.StdEncoding.DecodeString(parts[1]); err == nil {
					if part, err := writer.CreateFormFile(key, parts[0]); err == nil {
						part.Write(data)
						return
					}
				}
			}
		}
		// 普通字段
		writer.WriteField(key, strVal)
	}

	for k, v := range params {
		// 数组值(如多张参考图): 同名字段写多份
		switch arr := v.(type) {
		case []any:
			for _, item := range arr {
				writeFileOrField(k, fmt.Sprintf("%v", item))
			}
			continue
		case []string:
			for _, item := range arr {
				writeFileOrField(k, item)
			}
			continue
		}
		writeFileOrField(k, fmt.Sprintf("%v", v))
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
	params := req.Params

	// 图生图自动路由(配置驱动): 端点配了 image_edit 且请求带参考图字段
	// → 切 ImageEditPath 路径 + 强制 multipart 文件上传。
	// 未配 image_edit 的渠道(豆包/duomi)保持原路径/JSON,由 field_mapping 透传图 URL,不受影响。
	effectivePath := p.SubmitPath
	effectiveContentType := p.ContentType
	if p.hasImageEditInput(params) {
		effectivePath = p.ImageEditPath
		effectiveContentType = "multipart/form-data"
	}

	// 路径模板变量替换
	submitPath := resolvePath(effectivePath, params)
	reqURL := p.appendQueryAuth(p.BaseURL + submitPath)

	// body 认证：将 token 注入到请求参数中
	if p.AuthLocation == "body" {
		if params == nil {
			params = make(map[string]any)
		}
		params[p.AuthKey] = p.AuthValuePrefix + p.APIKey
	}

	// 流式判定: 交互模式=stream(p.Streaming) 或已注入 stream 参数(兼容旧 fixed_params)。
	// 为真则强制 params["stream"]=true(真布尔),覆盖 fixed_params 可能存成的字符串 "true",
	// 避免上游因类型不符拒绝。必须在 buildRequestBody 之前注入。
	streaming := p.Streaming || params["stream"] == true
	if streaming {
		if params == nil {
			params = make(map[string]any)
		}
		params["stream"] = true
		// 上游(gpt-image 系)仅在设了 partial_images 时才在生成途中吐 partial 事件字节;
		// 缺它则整段生成静默,挡在中间的 CDN(如 Cloudflare 100s 空闲阈值)判定源站无响应 → 524。
		// 缺省注入 1(与参考实现一致),已显式配置则尊重。真整数,避免 fixed_params 字符串坑。
		if _, ok := params["partial_images"]; !ok {
			params["partial_images"] = 1
		}
	}

	body, contentType := p.buildRequestBody(params, effectiveContentType)

	method := p.RequestMethod
	if method == "" {
		method = "POST"
	}

	// 流式请求上游持续推 SSE 字节流, http.Client.Timeout 是整请求硬超时(含读 body),
	// 会中途砍断流,故置 0; 改由 ctx deadline 兜底防永久挂起(endpoint.Timeout 派生,>0 时生效)。
	if streaming && p.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.Timeout)*time.Second)
		defer cancel()
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", contentType)
	p.setAuth(httpReq)

	client := sharedHTTPClient
	switch {
	case streaming:
		c := *sharedHTTPClient
		c.Timeout = 0
		client = &c
	case p.Timeout > 0 && time.Duration(p.Timeout)*time.Second > client.Timeout:
		c := *sharedHTTPClient
		c.Timeout = time.Duration(p.Timeout) * time.Second
		client = &c
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	// SSE 流式响应: 边读边聚合,从 completed 事件提取图片(错误也走 SSE error 事件,
	// 故先处理 status>=400 再交给流解析)。非流式渠道响应 application/json,不进此分支。
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		if resp.StatusCode >= 400 {
			respBody, _ := io.ReadAll(resp.Body)
			return SubmitResult{}, &domain.UpstreamError{StatusCode: resp.StatusCode, Body: extractUpstreamErrorMessage(respBody)}
		}
		return parseImageSSEStream(resp.Body)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return SubmitResult{}, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return SubmitResult{}, &domain.UpstreamError{StatusCode: resp.StatusCode, Body: extractUpstreamErrorMessage(respBody)}
	}

	return p.Parser.ParseSubmitResponse(respBody, p.ResponseMapping)
}

func (p *BaseProvider) GetProgress(ctx context.Context, providerTaskID string) (ProgressResult, error) {
	progressPath := resolvePath(p.ProgressPath, map[string]any{"task_id": providerTaskID, "id": providerTaskID})
	reqURL := p.appendQueryAuth(p.BaseURL + progressPath)

	method := p.PollMethod
	if method == "" {
		method = "GET"
	}

	var body io.Reader
	contentType := "application/json"
	if method == "POST" {
		params := map[string]any{"task_id": providerTaskID}
		if p.AuthLocation == "body" {
			params[p.AuthKey] = p.AuthValuePrefix + p.APIKey
		}
		bodyBytes, _ := json.Marshal(params)
		body = bytes.NewReader(bodyBytes)
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, reqURL, body)
	if err != nil {
		return ProgressResult{}, fmt.Errorf("create request: %w", err)
	}

	if method == "POST" {
		httpReq.Header.Set("Content-Type", contentType)
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
		// 返回结构化错误(保留 status code),交由 poll_worker 分级决定继续轮询或快速失败
		return ProgressResult{}, &domain.UpstreamError{StatusCode: resp.StatusCode, Body: extractUpstreamErrorMessage(respBody)}
	}

	mapping := p.PollResponseMapping
	if mapping.IsEmpty() {
		mapping = p.ResponseMapping
	}
	return p.Parser.ParseProgressResponse(respBody, mapping)
}

// ParseCallback 使用独立的 CallbackMapping 解析回调
func (p *BaseProvider) ParseCallback(ctx context.Context, body []byte) (ProgressResult, string, error) {
	// 优先使用 CallbackMapping，未配置（nil 或空对象 {}）则回退到 ResponseMapping
	mapping := p.CallbackMapping
	if mapping.IsEmpty() {
		mapping = p.ResponseMapping
	}

	return p.Parser.ParseCallbackResponse(body, mapping)
}
