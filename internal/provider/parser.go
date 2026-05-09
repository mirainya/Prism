package provider

import (
	"encoding/json"

	"github.com/tidwall/gjson"
)

type ResponseMapping struct {
	TaskID        string            `json:"task_id"`
	Status        string            `json:"status"`
	Progress      string            `json:"progress"`
	OutputURL     string            `json:"output_url"`
	Error         string            `json:"error"`
	StatusMapping map[string]string `json:"status_mapping"`
}

type ResponseParser interface {
	ParseSubmitResponse(body []byte, mapping *ResponseMapping) (SubmitResult, error)
	ParseProgressResponse(body []byte, mapping *ResponseMapping) (ProgressResult, error)
	ParseCallbackResponse(body []byte, mapping *ResponseMapping) (ProgressResult, string, error)
}

type DefaultParser struct{}

func NewDefaultParser() *DefaultParser {
	return &DefaultParser{}
}

// extractURLs 从 gjson 结果中提取 URL 列表，支持单值和数组
func extractURLs(jsonStr, path string) []string {
	result := gjson.Get(jsonStr, path)
	if !result.Exists() {
		return nil
	}

	// 如果结果是数组，提取每个元素
	if result.IsArray() {
		var urls []string
		result.ForEach(func(_, value gjson.Result) bool {
			if s := value.String(); s != "" {
				urls = append(urls, s)
			}
			return true
		})
		return urls
	}

	// 单值
	if s := result.String(); s != "" {
		return []string{s}
	}
	return nil
}

func (p *DefaultParser) ParseSubmitResponse(body []byte, mapping *ResponseMapping) (SubmitResult, error) {
	var result SubmitResult

	if mapping == nil {
		return result, nil
	}

	jsonStr := string(body)

	if mapping.TaskID != "" {
		result.ProviderTaskID = gjson.Get(jsonStr, mapping.TaskID).String()
	}

	if mapping.Status != "" {
		rawStatus := gjson.Get(jsonStr, mapping.Status).String()
		result.Status = p.mapStatus(rawStatus, mapping.StatusMapping)
	}

	if mapping.Progress != "" {
		result.Progress = int(gjson.Get(jsonStr, mapping.Progress).Int())
	}

	if mapping.OutputURL != "" {
		result.URLs = extractURLs(jsonStr, mapping.OutputURL)
	}

	return result, nil
}

func (p *DefaultParser) ParseProgressResponse(body []byte, mapping *ResponseMapping) (ProgressResult, error) {
	var result ProgressResult

	if mapping == nil {
		return result, nil
	}

	jsonStr := string(body)

	if mapping.Status != "" {
		rawStatus := gjson.Get(jsonStr, mapping.Status).String()
		result.Status = p.mapStatus(rawStatus, mapping.StatusMapping)
	}

	if mapping.Progress != "" {
		result.Progress = int(gjson.Get(jsonStr, mapping.Progress).Int())
	}

	if mapping.OutputURL != "" {
		result.URLs = extractURLs(jsonStr, mapping.OutputURL)
	}

	if mapping.Error != "" {
		result.Error = gjson.Get(jsonStr, mapping.Error).String()
	}

	return result, nil
}

// ParseCallbackResponse 解析回调请求体，返回进度结果和 provider_task_id
func (p *DefaultParser) ParseCallbackResponse(body []byte, mapping *ResponseMapping) (ProgressResult, string, error) {
	var result ProgressResult
	var providerTaskID string

	if mapping == nil {
		return result, "", nil
	}

	jsonStr := string(body)

	if mapping.TaskID != "" {
		providerTaskID = gjson.Get(jsonStr, mapping.TaskID).String()
	}

	if mapping.Status != "" {
		rawStatus := gjson.Get(jsonStr, mapping.Status).String()
		result.Status = p.mapStatus(rawStatus, mapping.StatusMapping)
	}

	if mapping.Progress != "" {
		result.Progress = int(gjson.Get(jsonStr, mapping.Progress).Int())
	}

	if mapping.OutputURL != "" {
		result.URLs = extractURLs(jsonStr, mapping.OutputURL)
	}

	if mapping.Error != "" {
		result.Error = gjson.Get(jsonStr, mapping.Error).String()
	}

	return result, providerTaskID, nil
}

func (p *DefaultParser) mapStatus(raw string, mapping map[string]string) TaskStatus {
	if mapping == nil {
		return TaskStatus(raw)
	}
	if mapped, ok := mapping[raw]; ok {
		return TaskStatus(mapped)
	}
	return TaskStatus(raw)
}

func ParseResponseMapping(data []byte) (*ResponseMapping, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var mapping ResponseMapping
	if err := json.Unmarshal(data, &mapping); err != nil {
		return nil, err
	}
	return &mapping, nil
}
