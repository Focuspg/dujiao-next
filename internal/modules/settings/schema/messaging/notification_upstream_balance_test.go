package settingsmessaging

import (
	"errors"
	"testing"

	"github.com/dujiao-next/internal/shared/jsonmap"
)

func TestUpstreamBalanceAlertDefaultsAreNotPrefilled(t *testing.T) {
	setting := NotificationCenterDefaultSetting()
	if setting.UpstreamBalanceAlertEnabled {
		t.Fatal("upstream balance alert must be disabled by default")
	}
	if setting.UpstreamBalanceAlertThresholdUSD != "" || setting.UpstreamCardBalanceAlertThresholdUSD != "" {
		t.Fatalf("thresholds must default to empty, got account=%q card=%q", setting.UpstreamBalanceAlertThresholdUSD, setting.UpstreamCardBalanceAlertThresholdUSD)
	}
	if setting.UpstreamBalanceAlertCheckMinutes != 30 {
		t.Fatalf("check interval default = %d, want 30", setting.UpstreamBalanceAlertCheckMinutes)
	}
	if setting.UpstreamBalanceAlertRepeatHours != 6 {
		t.Fatalf("repeat interval default = %d, want 6", setting.UpstreamBalanceAlertRepeatHours)
	}
	if _, ok := setting.UpstreamAccountBalanceThreshold(); ok {
		t.Fatal("empty account threshold must not be checked")
	}
	if _, ok := setting.UpstreamCardBalanceThreshold(); ok {
		t.Fatal("empty card threshold must not be checked")
	}
}

func TestUpstreamBalanceAlertNormalize(t *testing.T) {
	setting := NotificationCenterDefaultSetting()
	setting.UpstreamBalanceAlertThresholdUSD = " 5 "
	setting.UpstreamCardBalanceAlertThresholdUSD = "0"
	setting.UpstreamBalanceAlertCheckMinutes = 1
	setting.UpstreamBalanceAlertRepeatHours = 1000
	normalized := NormalizeNotificationCenterSetting(setting)
	if normalized.UpstreamBalanceAlertThresholdUSD != "5.00" {
		t.Fatalf("account threshold = %q", normalized.UpstreamBalanceAlertThresholdUSD)
	}
	if normalized.UpstreamCardBalanceAlertThresholdUSD != "" {
		t.Fatalf("zero card threshold must normalize to empty, got %q", normalized.UpstreamCardBalanceAlertThresholdUSD)
	}
	if normalized.UpstreamBalanceAlertCheckMinutes != 30 || normalized.UpstreamBalanceAlertRepeatHours != 6 {
		t.Fatalf("out-of-range intervals must fall back to defaults, got %d/%d", normalized.UpstreamBalanceAlertCheckMinutes, normalized.UpstreamBalanceAlertRepeatHours)
	}
	threshold, ok := normalized.UpstreamAccountBalanceThreshold()
	if !ok || threshold.String() != "5" {
		t.Fatalf("account threshold = %s ok=%v", threshold, ok)
	}
	if NormalizeNotificationUpstreamBalanceThreshold("abc") != "" || NormalizeNotificationUpstreamBalanceThreshold("-1") != "" {
		t.Fatal("invalid stored thresholds must normalize to empty (not checked)")
	}
}

func TestUpstreamBalanceAlertPatchValidatesThreshold(t *testing.T) {
	current := NotificationCenterDefaultSetting()
	enabled := true
	account := "12.345"
	card := "3"
	check := 10
	repeat := 12
	next, err := ApplyNotificationCenterSettingPatch(current, NotificationCenterSettingPatch{
		UpstreamBalanceAlertEnabled:          &enabled,
		UpstreamBalanceAlertThresholdUSD:     &account,
		UpstreamCardBalanceAlertThresholdUSD: &card,
		UpstreamBalanceAlertCheckMinutes:     &check,
		UpstreamBalanceAlertRepeatHours:      &repeat,
	})
	if err != nil {
		t.Fatalf("apply patch: %v", err)
	}
	if !next.UpstreamBalanceAlertEnabled || next.UpstreamBalanceAlertThresholdUSD != "12.35" || next.UpstreamCardBalanceAlertThresholdUSD != "3.00" ||
		next.UpstreamBalanceAlertCheckMinutes != 10 || next.UpstreamBalanceAlertRepeatHours != 12 {
		t.Fatalf("unexpected patched setting: %+v", next)
	}

	empty := ""
	cleared, err := ApplyNotificationCenterSettingPatch(next, NotificationCenterSettingPatch{UpstreamCardBalanceAlertThresholdUSD: &empty})
	if err != nil {
		t.Fatalf("clear threshold: %v", err)
	}
	if cleared.UpstreamCardBalanceAlertThresholdUSD != "" || cleared.UpstreamBalanceAlertThresholdUSD != "12.35" {
		t.Fatalf("clearing card threshold must keep account threshold: %+v", cleared)
	}

	for _, invalid := range []string{"-1", "abc", "1000001"} {
		value := invalid
		if _, err := ApplyNotificationCenterSettingPatch(current, NotificationCenterSettingPatch{UpstreamBalanceAlertThresholdUSD: &value}); !errors.Is(err, ErrNotificationConfigInvalid) {
			t.Fatalf("threshold %q: expected ErrNotificationConfigInvalid, got %v", invalid, err)
		}
	}
}

func TestUpstreamBalanceAlertRoundTripThroughStorageMap(t *testing.T) {
	setting := NotificationCenterDefaultSetting()
	setting.UpstreamBalanceAlertEnabled = true
	setting.UpstreamBalanceAlertThresholdUSD = "5"
	setting.UpstreamCardBalanceAlertThresholdUSD = "20.5"
	setting.UpstreamBalanceAlertCheckMinutes = 15
	setting.UpstreamBalanceAlertRepeatHours = 3

	raw := jsonmap.JSON(NotificationCenterSettingToMap(setting))
	if raw["upstream_balance_alert_threshold_usd"] != "5.00" || raw["upstream_card_balance_alert_threshold_usd"] != "20.50" {
		t.Fatalf("thresholds must be stored as fixed two-decimal strings: %v / %v", raw["upstream_balance_alert_threshold_usd"], raw["upstream_card_balance_alert_threshold_usd"])
	}
	decoded := NormalizeNotificationCenterSetting(DecodeNotificationCenterSetting(raw, NotificationCenterDefaultSetting()))
	if !decoded.UpstreamBalanceAlertEnabled || decoded.UpstreamBalanceAlertThresholdUSD != "5.00" || decoded.UpstreamCardBalanceAlertThresholdUSD != "20.50" ||
		decoded.UpstreamBalanceAlertCheckMinutes != 15 || decoded.UpstreamBalanceAlertRepeatHours != 3 {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}

	numeric := NormalizeNotificationCenterSetting(DecodeNotificationCenterSetting(jsonmap.JSON{
		"upstream_balance_alert_threshold_usd": float64(7),
	}, NotificationCenterDefaultSetting()))
	if numeric.UpstreamBalanceAlertThresholdUSD != "7.00" {
		t.Fatalf("numeric stored threshold = %q", numeric.UpstreamBalanceAlertThresholdUSD)
	}
}
