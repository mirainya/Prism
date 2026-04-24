package provider

import (
	"encoding/json"
	"strings"

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

// normalizeGjsonPath 将 [N] 数组语法转为 gjson 的 .N 语法
func normalizeGjsonPath(path string) string {
	for {
		i := strings.Index(path, "[")
		if i < 0 {
			return path
		}
		j := strings.Index(path[i:], "]")
		if j < 0 {
			return path
		}
		path = path[:i] + "." + path[i+1:i+j] + path[i+j+1:]
	}
}

// extractURLs 从 gjson 结果中提取 URL 列表，支持单值和数组
func extractURLs(jsonStr, path string) []string {
	result := gjson.Get(jsonStr, normalizeGjsonPath(path))
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
		result.ProviderTaskID = gjson.Get(jsonStr, normalizeGjsonPath(mapping.TaskID)).String()
	}

	if mapping.Status != "" {
		rawStatus := gjson.Get(jsonStr, normalizeGjsonPath(mapping.Status)).String()
		result.Status = p.mapStatus(rawStatus, mapping.StatusMapping)
	}

	if mapping.Progress != "" {
		result.Progress = int(gjson.Get(jsonStr, normalizeGjsonPath(mapping.Progress)).Int())
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
		rawStatus := gjson.Get(jsonStr, normalizeGjsonPath(mapping.Status)).String()
		result.Status = p.mapStatus(rawStatus, mapping.StatusMapping)
	}

	if mapping.Progress != "" {
		result.Progress = int(gjson.Get(jsonStr, normalizeGjsonPath(mapping.Progress)).Int())
	}

	if mapping.OutputURL != "" {
		result.URLs = extractURLs(jsonStr, mapping.OutputURL)
	}

	if mapping.Error != "" {
		result.Error = gjson.Get(jsonStr, normalizeGjsonPath(mapping.Error)).String()
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
		providerTaskID = gjson.Get(jsonStr, normalizeGjsonPath(mapping.TaskID)).String()
	}

	if mapping.Status != "" {
		rawStatus := gjson.Get(jsonStr, normalizeGjsonPath(mapping.Status)).String()
		result.Status = p.mapStatus(rawStatus, mapping.StatusMapping)
	}

	if mapping.Progress != "" {
		result.Progress = int(gjson.Get(jsonStr, normalizeGjsonPath(mapping.Progress)).Int())
	}

	if mapping.OutputURL != "" {
		result.URLs = extractURLs(jsonStr, mapping.OutputURL)
	}

	if mapping.Error != "" {
		result.Error = gjson.Get(jsonStr, normalizeGjsonPath(mapping.Error)).String()
	}

	return result, providerTaskID, nil
}

func (p *DefaultParser) mapStatus(raw string, mapping map[string]string) TaskStatus {
	if mapping == nil {
		return TaskStatus(strings.ToUpper(raw))
	}
	if mapped, ok := mapping[raw]; ok {
		return TaskStatus(strings.ToUpper(mapped))
	}
	return TaskStatus(strings.ToUpper(raw))
}

func ParseResponseMapping(data []byte) (*ResponseMapping, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var mapping ResponseMapping
	if err := json.Unmarshal(data, &mapping); err != nil {
		return nil, err
	}

	// 兼容新格式: {"field_mapping": {"task_id": "xxx", "status": "xxx", ...}, "value_mapping": {"status": {...}}}
	var raw struct {
		FieldMapping map[string]string            `json:"field_mapping"`
		ValueMapping map[string]map[string]string `json:"value_mapping"`
	}
	if err := json.Unmarshal(data, &raw); err == nil && len(raw.FieldMapping) > 0 {
		if v, ok := raw.FieldMapping["task_id"]; ok && mapping.TaskID == "" {
			mapping.TaskID = v
		}
		if v, ok := raw.FieldMapping["status"]; ok && mapping.Status == "" {
			mapping.Status = v
		}
		if v, ok := raw.FieldMapping["progress"]; ok && mapping.Progress == "" {
			mapping.Progress = v
		}
		if v, ok := raw.FieldMapping["url"]; ok && mapping.OutputURL == "" {
			mapping.OutputURL = v
		}
		if v, ok := raw.FieldMapping["urls"]; ok && mapping.OutputURL == "" {
			mapping.OutputURL = v
		}
		if v, ok := raw.FieldMapping["error"]; ok && mapping.Error == "" {
			mapping.Error = v
		}
		if sm, ok := raw.ValueMapping["status"]; ok && mapping.StatusMapping == nil {
			mapping.StatusMapping = sm
		}
	}

	return &mapping, nil
}
