package catalogsource

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mirainya/Prism/internal/gateway/billing"
)

// AICostAvailabilityPath is the provider's observed-availability report. It is
// deliberately not a catalog discovery contract: the figures it returns change
// every few minutes and must never enter a catalog release, whose whole purpose
// is to stay fixed long enough to be proven against a deployed build.
const AICostAvailabilityPath = "/api/dashboard/success_rate"

// maxAvailabilityWindow bounds the reported sliding window at one week so a
// malformed upstream value cannot be presented to users as a meaningful rate.
const maxAvailabilityWindow = 7 * 24 * 60

// UpstreamAvailability is one model's observed success rate. Rates are carried
// as decimal strings on the provider's own 0..100 percentage scale: converting
// here would make the stored number disagree with the provider's dashboard, and
// float arithmetic is not permitted anywhere in the billing-adjacent path.
//
// There is no sample count because the provider publishes none. A rate of 100%
// may therefore rest on a single call, which is why locally measured rates take
// precedence wherever both exist.
type UpstreamAvailability struct {
	ModelCode                string
	Category                 string
	SuccessRate              string
	AverageCompletionSeconds string
	WindowMinutes            uint32
	ObservedAt               time.Time
	HasData                  bool
}

type aicostAvailabilityRow struct {
	Model                    string      `json:"model"`
	SuccessRate              json.Number `json:"success_rate"`
	AverageCompletionSeconds json.Number `json:"average_completion_seconds"`
	Buckets                  []struct {
		HasData bool `json:"has_data"`
	} `json:"buckets"`
}

// ParseAICostAvailability reduces the provider report to the allowlisted fields.
// The per-bucket breakdown is intentionally discarded: only the window-wide
// figure is shown to users, and keeping the buckets would store a far larger
// payload that nothing reads.
func ParseAICostAvailability(value []byte, category string) ([]UpstreamAvailability, error) {
	if !validText(category, 32) {
		return nil, ErrInvalidSnapshot
	}
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    *struct {
			Category      string                  `json:"category"`
			WindowMinutes uint32                  `json:"window_minutes"`
			UpdatedAt     int64                   `json:"updated_at"`
			Rows          []aicostAvailabilityRow `json:"rows"`
		} `json:"data"`
	}
	if err := decode(value, &response); err != nil || !response.Success || response.Data == nil {
		return nil, ErrInvalidSnapshot
	}
	data := response.Data
	if strings.TrimSpace(data.Category) != category || data.WindowMinutes == 0 ||
		data.WindowMinutes > maxAvailabilityWindow || len(data.Rows) > 10000 {
		return nil, ErrInvalidSnapshot
	}
	// A zero or negative timestamp would render as a 1970 observation date and
	// silently present stale data as current.
	if data.UpdatedAt <= 0 {
		return nil, ErrInvalidSnapshot
	}
	observed := time.Unix(data.UpdatedAt, 0).UTC()
	items := make([]UpstreamAvailability, 0, len(data.Rows))
	seen := make(map[string]struct{}, len(data.Rows))
	for _, row := range data.Rows {
		item, err := normalizeAICostAvailability(row, category, data.WindowMinutes, observed)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[item.ModelCode]; exists {
			return nil, fmt.Errorf("%w: duplicate availability row %s", ErrInvalidSnapshot, item.ModelCode)
		}
		seen[item.ModelCode] = struct{}{}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ModelCode < items[j].ModelCode })
	return items, nil
}

func normalizeAICostAvailability(row aicostAvailabilityRow, category string, window uint32, observed time.Time) (UpstreamAvailability, error) {
	code := strings.TrimSpace(row.Model)
	if !validModelCode(code) {
		return UpstreamAvailability{}, ErrInvalidSnapshot
	}
	rate, err := boundedDecimal(row.SuccessRate, "100")
	if err != nil {
		return UpstreamAvailability{}, err
	}
	hasData := false
	for _, bucket := range row.Buckets {
		if bucket.HasData {
			hasData = true
			break
		}
	}
	if !hasData {
		return UpstreamAvailability{
			ModelCode: code, Category: category, WindowMinutes: window,
			ObservedAt: observed, HasData: false,
		}, nil
	}
	// The completion time is advisory, so an absurd value is dropped rather than
	// failing the whole report; the success rate is the field users act on.
	completion, err := boundedDecimal(row.AverageCompletionSeconds, "86400")
	if err != nil {
		completion = "0"
	}
	return UpstreamAvailability{
		ModelCode: code, Category: category, SuccessRate: rate,
		AverageCompletionSeconds: completion, WindowMinutes: window, ObservedAt: observed, HasData: true,
	}, nil
}

func boundedDecimal(number json.Number, upper string) (string, error) {
	if number == "" {
		return "", ErrInvalidSnapshot
	}
	amount, err := billing.ParseAmount(number.String(), 18, true)
	if err != nil || amount.Sign() < 0 {
		return "", ErrInvalidSnapshot
	}
	bound, err := billing.ParseAmount(upper, 18, true)
	if err != nil || amount.Cmp(bound) > 0 {
		return "", ErrInvalidSnapshot
	}
	return amount.String(), nil
}
