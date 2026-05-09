package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ParamTemplate struct {
	FieldMapping map[string]string         `json:"field_mapping"`
	ValueMapping map[string]map[string]any `json:"value_mapping"`
	Defaults     map[string]any            `json:"defaults"`
}

type ParamConverter interface {
	Convert(unified map[string]any, template *ParamTemplate) (map[string]any, error)
}

type DefaultConverter struct{}

func NewDefaultConverter() *DefaultConverter {
	return &DefaultConverter{}
}

// setNestedValue 根据点号路径设置嵌套值
// 例如 "input.prompt" → result["input"]["prompt"] = val
func setNestedValue(result map[string]any, path string, val any) {
	parts := strings.Split(path, ".")
	if len(parts) == 1 {
		result[path] = val
		return
	}

	current := result
	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		if existing, ok := current[key]; ok {
			if m, ok := existing.(map[string]any); ok {
				current = m
			} else {
				// 已有非 map 值，覆盖
				m := make(map[string]any)
				current[key] = m
				current = m
			}
		} else {
			m := make(map[string]any)
			current[key] = m
			current = m
		}
	}
	current[parts[len(parts)-1]] = val
}

func (c *DefaultConverter) Convert(unified map[string]any, tpl *ParamTemplate) (map[string]any, error) {
	if tpl == nil {
		return unified, nil
	}

	result := make(map[string]any)

	// 1. 应用默认值（支持嵌套路径）
	for k, v := range tpl.Defaults {
		setNestedValue(result, k, v)
	}

	// 2. 字段映射转换（支持嵌套路径，如 "prompt" → "input.prompt"）
	for unifiedKey, providerKey := range tpl.FieldMapping {
		val, ok := unified[unifiedKey]
		if !ok {
			continue
		}

		// 检查是否需要值映射
		if valueMap, hasMapping := tpl.ValueMapping[unifiedKey]; hasMapping {
			strVal := fmt.Sprint(val)
			if mappedVal, found := valueMap[strVal]; found {
				setNestedValue(result, providerKey, mappedVal)
				continue
			}
		}
		setNestedValue(result, providerKey, val)
	}

	// 3. 保留未映射的字段
	for k, v := range unified {
		if _, mapped := tpl.FieldMapping[k]; !mapped {
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}
	}

	return result, nil
}

func ParseParamTemplate(data []byte) (*ParamTemplate, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var tpl ParamTemplate
	if err := json.Unmarshal(data, &tpl); err != nil {
		return nil, err
	}
	return &tpl, nil
}
