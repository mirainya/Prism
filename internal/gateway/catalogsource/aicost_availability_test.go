package catalogsource

import (
	"testing"
	"time"
)

// The fixture mirrors the shape observed on the live provider, including the
// per-bucket breakdown that the parser is expected to ignore.
const availabilityFixture = `{"data":{"category":"video","window_minutes":120,"bucket_minutes":5,"updated_at":1789380174,
"rows":[
{"model":"seedance2.0-900-3","success_rate":88.3,"average_completion_seconds":495.3,
 "buckets":[{"label":"16:02","start":1789372974,"end":1789373274,"has_data":true,"has_failure":true,"success_rate":93.8}]},
{"model":"seedance2.0-fast-2","success_rate":100,"average_completion_seconds":169.1,"buckets":[]}
]},"message":"","success":true}`

func TestParseAICostAvailabilityKeepsProviderScale(t *testing.T) {
	items, err := ParseAICostAvailability([]byte(availabilityFixture), "video")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items=%d, want 2", len(items))
	}
	// Sorted by model code, so the fast variant comes first.
	if items[0].ModelCode != "seedance2.0-900-3" || items[1].ModelCode != "seedance2.0-fast-2" {
		t.Fatalf("unsorted result: %s, %s", items[0].ModelCode, items[1].ModelCode)
	}
	if items[0].SuccessRate != "88.3" {
		t.Fatalf("success rate=%q, want the provider's own 0..100 scale", items[0].SuccessRate)
	}
	if items[0].AverageCompletionSeconds != "495.3" {
		t.Fatalf("completion seconds=%q", items[0].AverageCompletionSeconds)
	}
	if items[0].WindowMinutes != 120 || items[0].Category != "video" {
		t.Fatalf("window=%d category=%q", items[0].WindowMinutes, items[0].Category)
	}
	if !items[0].HasData || items[1].HasData {
		t.Fatalf("sample flags were not preserved: %#v", items)
	}
	if !items[0].ObservedAt.Equal(time.Unix(1789380174, 0).UTC()) {
		t.Fatalf("observed at=%s", items[0].ObservedAt)
	}
}

func TestParseAICostAvailabilityTreatsMissingSamplesAsUnknown(t *testing.T) {
	payload := `{"data":{"category":"language","window_minutes":120,"updated_at":1789380174,"rows":[
{"model":"gpt-test","success_rate":0,"average_completion_seconds":0,
 "buckets":[{"has_data":false,"success_rate":0}]}
]},"message":"","success":true}`
	items, err := ParseAICostAvailability([]byte(payload), "language")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].HasData || items[0].SuccessRate != "" {
		t.Fatalf("no-sample row must remain unknown: %#v", items)
	}
}

func TestParseAICostAvailabilityRejectsBadReports(t *testing.T) {
	for name, payload := range map[string]string{
		"category mismatch": `{"success":true,"data":{"category":"image","window_minutes":120,"updated_at":1789380174,"rows":[]}}`,
		"zero window":       `{"success":true,"data":{"category":"video","window_minutes":0,"updated_at":1789380174,"rows":[]}}`,
		"absent timestamp":  `{"success":true,"data":{"category":"video","window_minutes":120,"updated_at":0,"rows":[]}}`,
		"rate above 100":    `{"success":true,"data":{"category":"video","window_minutes":120,"updated_at":1789380174,"rows":[{"model":"a","success_rate":101,"average_completion_seconds":1}]}}`,
		"negative rate":     `{"success":true,"data":{"category":"video","window_minutes":120,"updated_at":1789380174,"rows":[{"model":"a","success_rate":-1,"average_completion_seconds":1}]}}`,
		"duplicate model":   `{"success":true,"data":{"category":"video","window_minutes":120,"updated_at":1789380174,"rows":[{"model":"a","success_rate":1,"average_completion_seconds":1},{"model":"a","success_rate":2,"average_completion_seconds":1}]}}`,
		"unsuccessful":      `{"success":false,"data":{"category":"video","window_minutes":120,"updated_at":1789380174,"rows":[]}}`,
		"absent data":       `{"success":true,"message":"Unauthorized"}`,
	} {
		if _, err := ParseAICostAvailability([]byte(payload), "video"); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

// An unusable completion time must not discard the success rate, which is the
// figure the routing order and the user-facing display both depend on.
func TestParseAICostAvailabilityToleratesBadCompletionTime(t *testing.T) {
	payload := `{"success":true,"data":{"category":"video","window_minutes":120,"updated_at":1789380174,
"rows":[{"model":"a","success_rate":50,"average_completion_seconds":999999999,"buckets":[{"has_data":true}]}]}}`
	items, err := ParseAICostAvailability([]byte(payload), "video")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SuccessRate != "50" || items[0].AverageCompletionSeconds != "0" {
		t.Fatalf("unexpected item: %+v", items)
	}
}
