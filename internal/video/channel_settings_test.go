package video

import (
	"testing"

	"gorm.io/datatypes"
)

func TestVideoChannelFormalSettingsTakePrecedence(t *testing.T) {
	channel := &VideoChannel{
		AdapterType:           AdapterTypeGeneric,
		AdapterProfile:        "formal_profile",
		RequestTimeoutSeconds: 45,
		SupportsFirstFrame:    boolPointer(false),
		CancelMode:            CancelModeDisabled,
		ResultStorageEnabled:  boolPointer(false),
		Capabilities:          datatypes.JSON(`{"first_frame":true,"cancel":true}`),
		ExtraConfig: datatypes.JSON(`{
			"adapter":{"profile":"legacy_profile","timeout_seconds":90,"cancel":{"enabled":true}},
			"result_storage":{"enabled":true}
		}`),
	}

	if channel.Capability("first_frame") || channel.Capability("cancel") {
		t.Fatal("formal capability and cancellation settings must take precedence")
	}
	if got := channel.EffectiveAdapterProfile(); got != "formal_profile" {
		t.Fatalf("adapter profile=%q", got)
	}
	if got := channel.EffectiveRequestTimeoutSeconds(); got != 45 {
		t.Fatalf("request timeout=%d", got)
	}
	if channel.EffectiveResultStorageEnabled() {
		t.Fatal("formal result storage setting must take precedence")
	}
}

func TestVideoChannelLegacySettingsRemainReadable(t *testing.T) {
	channel := &VideoChannel{
		AdapterType:  AdapterTypeGeneric,
		Capabilities: datatypes.JSON(`{"first_frame":true,"audio":true}`),
		ExtraConfig: datatypes.JSON(`{
			"adapter":{"profile":"json_task_v1","timeout_seconds":75,"cancel":{"enabled":true}},
			"result_storage":{"enabled":true}
		}`),
	}

	if !channel.Capability("first_frame") || !channel.Capability("audio") {
		t.Fatal("legacy capabilities were not read")
	}
	if got := channel.EffectiveCancelMode(); got != CancelModeProvider {
		t.Fatalf("cancel mode=%q", got)
	}
	if got := channel.EffectiveAdapterProfile(); got != "json_task_v1" {
		t.Fatalf("adapter profile=%q", got)
	}
	if got := channel.EffectiveRequestTimeoutSeconds(); got != 75 {
		t.Fatalf("request timeout=%d", got)
	}
	if !channel.EffectiveResultStorageEnabled() {
		t.Fatal("legacy result storage setting was not read")
	}
}

func TestVideoChannelCancelModes(t *testing.T) {
	for _, mode := range []string{CancelModeDisabled, CancelModeLocalOnly, CancelModeProvider} {
		channel := &VideoChannel{AdapterType: AdapterTypeSeedance, CancelMode: mode}
		if got := channel.EffectiveCancelMode(); got != mode {
			t.Fatalf("cancel mode=%q, want %q", got, mode)
		}
		if got := channel.Capability("cancel"); got != (mode == CancelModeProvider) {
			t.Fatalf("cancel capability=%t for mode %q", got, mode)
		}
	}
}
