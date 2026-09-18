package adapter

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mirainya/Prism/internal/gateway/billing"
	"github.com/mirainya/Prism/internal/video"
)

// videoBillingFacts converts facts that are available at the adapter boundary
// into the expression environment used by released video prices. The declared
// set is intentionally limited to values this adapter can prove from the
// request/result; missing values remain missing rather than being guessed.
func videoBillingFacts(input VideoRequest, generatedSeconds string, success bool) (billing.ExprEnv, error) {
	declared := map[string]bool{
		"success":       true,
		"duration_ms":   true,
		"attempt_count": true,
		"seconds":       true,
		"resolution":    true,
		"has_audio":     true,
		"has_video_ref": true,
		"priority":      true,
		"task_mode":     true,
	}
	vars := make(map[string]billing.ExprValue, len(declared))
	putNumber := func(name, value string) error {
		amount, err := billing.ParseAmount(value, 18, true)
		if err != nil {
			return fmt.Errorf("billing variable %s: %w", name, err)
		}
		vars[name] = billing.ExprNumber(amount)
		return nil
	}
	if err := putNumber("success", boolNumber(success)); err != nil {
		return billing.ExprEnv{}, err
	}
	if err := putNumber("has_audio", boolNumber(input.GenerateAudio)); err != nil {
		return billing.ExprEnv{}, err
	}
	if err := putNumber("has_video_ref", boolNumber(hasVideoReference(input.Content))); err != nil {
		return billing.ExprEnv{}, err
	}
	seconds := strings.TrimSpace(generatedSeconds)
	if seconds == "" && input.Duration > 0 {
		seconds = strconv.Itoa(input.Duration)
	}
	if seconds != "" {
		if err := putNumber("seconds", seconds); err != nil {
			return billing.ExprEnv{}, err
		}
	}
	if value := strings.TrimSpace(input.Resolution); value != "" {
		vars["resolution"] = billing.ExprString(value)
	}
	if value := strings.TrimSpace(input.TaskMode); value != "" {
		vars["task_mode"] = billing.ExprString(value)
	}
	if value, ok, err := billingParamNumber(input.Params, "priority"); err != nil {
		return billing.ExprEnv{}, err
	} else if ok {
		vars["priority"] = billing.ExprNumber(value)
	}
	return billing.ExprEnv{Declared: declared, Vars: vars}, nil
}

// billingParamNumber converts a JSON extension parameter without going
// through float arithmetic. Request bodies decoded by encoding/json normally
// contain float64 values, so the integral check is deliberate: priority is a
// discrete manifest value and must not silently become a rounded decimal.
func billingParamNumber(params map[string]any, name string) (billing.Amount, bool, error) {
	raw, ok := params[name]
	if !ok {
		return billing.Zero(), false, nil
	}
	var text string
	switch value := raw.(type) {
	case json.Number:
		text = value.String()
	case string:
		text = strings.TrimSpace(value)
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return billing.Amount{}, false, fmt.Errorf("billing variable %s: invalid number", name)
		}
		text = strconv.FormatFloat(value, 'f', -1, 64)
	case float32:
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return billing.Amount{}, false, fmt.Errorf("billing variable %s: invalid number", name)
		}
		text = strconv.FormatFloat(float64(value), 'f', -1, 32)
	case int:
		text = strconv.Itoa(value)
	case int8:
		text = strconv.FormatInt(int64(value), 10)
	case int16:
		text = strconv.FormatInt(int64(value), 10)
	case int32:
		text = strconv.FormatInt(int64(value), 10)
	case int64:
		text = strconv.FormatInt(value, 10)
	case uint:
		text = strconv.FormatUint(uint64(value), 10)
	case uint8:
		text = strconv.FormatUint(uint64(value), 10)
	case uint16:
		text = strconv.FormatUint(uint64(value), 10)
	case uint32:
		text = strconv.FormatUint(uint64(value), 10)
	case uint64:
		text = strconv.FormatUint(value, 10)
	default:
		return billing.Amount{}, false, fmt.Errorf("billing variable %s: invalid number", name)
	}
	amount, err := billing.ParseAmount(text, 18, true)
	if err != nil || amount.Scale() != 0 {
		if err == nil {
			err = billing.ErrInvalidAmount
		}
		return billing.Amount{}, false, fmt.Errorf("billing variable %s: %w", name, err)
	}
	return amount, true, nil
}

func boolNumber(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func hasVideoReference(content []video.ContentItem) bool {
	for _, item := range content {
		switch strings.ToLower(strings.TrimSpace(item.Type)) {
		case "video_url", "video", "input_video", "output_video":
			return true
		}
	}
	return false
}

func imageBillingFacts(observation OpenAIImagesObservation) (billing.ExprEnv, error) {
	declared := map[string]bool{
		"success":       true,
		"duration_ms":   true,
		"attempt_count": true,
		"count":         true,
		"input_tokens":  true,
		"output_tokens": true,
	}
	vars := make(map[string]billing.ExprValue, len(declared))
	putNumber := func(name, value string) error {
		amount, err := billing.ParseAmount(value, 18, true)
		if err != nil {
			return fmt.Errorf("billing variable %s: %w", name, err)
		}
		vars[name] = billing.ExprNumber(amount)
		return nil
	}
	if err := putNumber("success", "1"); err != nil {
		return billing.ExprEnv{}, err
	}
	if err := putNumber("count", strconv.Itoa(len(observation.Outputs))); err != nil {
		return billing.ExprEnv{}, err
	}
	if observation.Usage.InputTokens != nil {
		if err := putNumber("input_tokens", strconv.FormatInt(*observation.Usage.InputTokens, 10)); err != nil {
			return billing.ExprEnv{}, err
		}
	}
	if observation.Usage.OutputTokens != nil {
		if err := putNumber("output_tokens", strconv.FormatInt(*observation.Usage.OutputTokens, 10)); err != nil {
			return billing.ExprEnv{}, err
		}
	}
	return billing.ExprEnv{Declared: declared, Vars: vars}, nil
}
