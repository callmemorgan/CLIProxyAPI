package management

import (
	"encoding/json"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRequestedUsageProviders(t *testing.T) {
	got := requestedUsageProviders("kimi, agy,codex,kimi,unknown")
	want := []string{"kimi", "antigravity", "codex"}
	if len(got) != len(want) {
		t.Fatalf("providers = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("providers = %#v, want %#v", got, want)
		}
	}
}

func TestNormalizeKimiWindowFromRemaining(t *testing.T) {
	got, ok := normalizeKimiWindow("weekly", "Kimi weekly", map[string]any{
		"limit":     float64(1000),
		"remaining": float64(750),
		"resetTime": "2026-07-17T03:03:00Z",
	})
	if !ok {
		t.Fatal("normalizeKimiWindow returned false")
	}
	if got.Used != 250 || got.Percent != 25 || got.ResetAt != "2026-07-17T03:03:00Z" {
		t.Fatalf("window = %#v", got)
	}
}

func TestObservedProviderUsageMapsGrokToXAI(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	got := observedProviderUsage("grok", []*coreauth.Auth{{Provider: "xai"}}, now)
	if got.State != "available" || got.Mode != "observed" || got.Error != "" {
		t.Fatalf("usage = %#v", got)
	}
}

func TestNormalizeGrokImplicitZeroWeeklyWindow(t *testing.T) {
	got := normalizeGrokWindows(map[string]any{"currentPeriod": map[string]any{
		"type": "USAGE_PERIOD_TYPE_WEEKLY", "end": "2026-07-18T03:03:02Z",
	}})
	if len(got) != 1 || got[0].ID != "weekly" || got[0].Percent != 0 {
		t.Fatalf("windows = %#v", got)
	}
}

func TestNormalizeCodexWindows(t *testing.T) {
	got := normalizeCodexWindows(map[string]any{"rate_limit": map[string]any{
		"primary_window":   map[string]any{"used_percent": float64(5), "reset_at": float64(1783832346)},
		"secondary_window": map[string]any{"used_percent": float64(7), "reset_at": "1784400936"},
	}})
	if len(got) != 2 {
		t.Fatalf("windows = %#v", got)
	}
	if got[0].ID != "5h" || got[0].Label != "Codex 5h" || got[0].Percent != 5 || got[0].ResetAt == "" {
		t.Fatalf("primary = %#v", got[0])
	}
	if got[1].ID != "weekly" || got[1].Label != "Codex weekly" || got[1].Percent != 7 || got[1].ResetAt == "" {
		t.Fatalf("secondary = %#v", got[1])
	}
}

func TestNormalizeCodexWindowsAcceptsAlternateShape(t *testing.T) {
	got := normalizeCodexWindows(map[string]any{"rate_limits": map[string]any{
		"primary": map[string]any{"used_percent": float64(19), "resets_at": "2026-07-12T18:00:00Z"},
	}})
	if len(got) != 1 || got[0].Percent != 19 || got[0].ResetAt != "2026-07-12T18:00:00Z" {
		t.Fatalf("windows = %#v", got)
	}
}

func TestNormalizeCodexWindowsUsesReportedDuration(t *testing.T) {
	got := normalizeCodexWindows(map[string]any{"rate_limit": map[string]any{
		"primary_window": map[string]any{
			"used_percent":         float64(1),
			"limit_window_seconds": float64(7 * 24 * 60 * 60),
			"reset_at":             float64(1784514654),
		},
		"secondary_window": nil,
	}})
	if len(got) != 1 {
		t.Fatalf("windows = %#v, want one weekly window", got)
	}
	if got[0].ID != "weekly" || got[0].Label != "Codex weekly" || got[0].Percent != 1 {
		t.Fatalf("window = %#v, want Codex weekly at 1%%", got[0])
	}
}

func TestNormalizeAntigravityWindowsGroupsModelsAndConvertsRemaining(t *testing.T) {
	now := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)
	fraction := func(value float64) *float64 { return &value }
	got := normalizeAntigravityWindows([]antigravityQuotaBucket{
		{ModelID: "gemini-3.5-flash", RemainingFraction: fraction(.77), ResetTime: "2026-07-14T02:05:00Z"},
		{ModelID: "gemini-3.5-pro", RemainingFraction: fraction(.80), ResetTime: "2026-07-14T02:05:00Z"},
		{ModelID: "gemini-3.5-flash", RemainingFraction: fraction(.9785), ResetTime: "2026-07-12T18:53:00Z"},
		{ModelID: "claude-sonnet", RemainingFraction: fraction(.0051), ResetTime: "2026-07-14T04:19:00Z"},
		{ModelID: "gpt-oss", RemainingFraction: fraction(1), ResetTime: "2026-07-12T21:00:00Z"},
	}, now)
	if len(got) != 4 {
		t.Fatalf("windows = %#v", got)
	}
	want := []struct {
		id      string
		percent float64
	}{{"gemini-weekly", 23}, {"gemini-5h", 2.15}, {"claude-gpt-weekly", 99.49}, {"claude-gpt-5h", 0}}
	for index, expected := range want {
		if got[index].ID != expected.id || got[index].Percent < expected.percent-.001 || got[index].Percent > expected.percent+.001 {
			t.Errorf("window %d = %#v, want %s %.2f", index, got[index], expected.id, expected.percent)
		}
	}
}

func TestNormalizeAntigravitySummary(t *testing.T) {
	var summary antigravityQuotaSummaryResponse
	err := json.Unmarshal([]byte(`{"response":{"groups":[
	  {"displayName":"Gemini Models","buckets":[
	    {"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.77,"resetTime":"2026-07-14T02:33:13Z"},
	    {"bucketId":"gemini-5h","window":"5h","remainingFraction":0.9785,"resetTime":"2026-07-12T19:20:51Z"}]},
	  {"displayName":"Claude and GPT models","buckets":[
	    {"bucketId":"3p-weekly","window":"weekly","remainingFraction":0.0051,"resetTime":"2026-07-14T04:47:19Z"},
	    {"bucketId":"3p-5h","window":"5h","remainingFraction":1,"resetTime":"2026-07-12T21:34:17Z"}]}
	]}}`), &summary)
	if err != nil {
		t.Fatal(err)
	}
	got := normalizeAntigravitySummary(summary)
	if len(got) != 4 {
		t.Fatalf("windows = %#v", got)
	}
	if got[0].ID != "gemini-weekly" || got[0].Percent != 23 || got[2].ID != "3p-weekly" || got[2].Percent < 99.48 {
		t.Fatalf("windows = %#v", got)
	}
}
