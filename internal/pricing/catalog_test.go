package pricing

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmbeddedCatalogCoversLiveSnapshot(t *testing.T) {
	want := strings.Fields(`
		claude-3-5-haiku-20241022 claude-3-7-sonnet-20250219 claude-fable-5
		claude-haiku-4-5-20251001 claude-opus-4-1-20250805 claude-opus-4-20250514
		claude-opus-4-5-20251101 claude-opus-4-6 claude-opus-4-6-thinking claude-opus-4-7
		claude-opus-4-8 claude-sonnet-4-20250514 claude-sonnet-4-5-20250929
		claude-sonnet-4-6 claude-sonnet-5 codex-auto-review gemini-3-flash
		gemini-3-flash-agent gemini-3.1-flash-image gemini-3.1-flash-lite gemini-3.1-pro-low
		gemini-3.5-flash-extra-low gemini-3.5-flash-low gemini-pro-agent glm-5.2
		gpt-5.3-codex-spark gpt-5.4 gpt-5.4-mini gpt-5.5 gpt-5.6-luna gpt-5.6-sol
		gpt-5.6-terra gpt-image-1.5 gpt-image-2 gpt-oss-120b-medium grok-3-mini
		grok-3-mini-fast grok-4.20-0309-non-reasoning grok-4.20-0309-reasoning
		grok-4.20-multi-agent-0309 grok-4.3 grok-4.5 grok-build-0.1 grok-composer-2.5-fast
		grok-imagine-image grok-imagine-image-quality grok-imagine-video
		grok-imagine-video-1.5-preview kimi-k2 kimi-k2-thinking kimi-k2.5 kimi-k2.6
		kimi-k2.7-code kimi-k2.7-code-highspeed kimi-k3 minimax-m3
	`)
	got := ModelIDs()
	if len(got) != 56 {
		t.Fatalf("catalog length = %d, want 56", len(got))
	}
	wantSet := make(map[string]bool, len(want))
	for _, id := range want {
		wantSet[id] = true
	}
	for _, id := range got {
		if !wantSet[id] {
			t.Errorf("unexpected catalog model %q", id)
		}
		delete(wantSet, id)
	}
	for id := range wantSet {
		t.Errorf("missing catalog model %q", id)
	}
}

func TestCatalogProjections(t *testing.T) {
	claude := Lookup("claude-opus-4-8")
	if claude.Status != StatusPriced || claude.Tokens == nil || *claude.Tokens.CacheWrite1h != 10 {
		t.Fatalf("unexpected Claude pricing: %#v", claude)
	}

	unavailable := Lookup("gpt-5.3-codex-spark")
	if unavailable.Status != StatusUnavailable || unavailable.UnavailableReason != "research_preview_unpriced" {
		t.Fatalf("unexpected unavailable pricing: %#v", unavailable)
	}

	tiered := Lookup("gpt-5.4")
	if len(tiered.Tokens.Tiers) != 1 || tiered.Tokens.Tiers[0].MinInputTokens != 272001 {
		t.Fatalf("unexpected tiered pricing: %#v", tiered.Tokens)
	}

	media := Lookup("gpt-image-1.5")
	if media.ImageTokens == nil || len(media.Images) != 6 {
		t.Fatalf("unexpected media pricing: %#v", media)
	}

	video := Lookup("grok-imagine-video")
	if len(video.Videos) != 2 || video.Videos[1].USD != 0.07 {
		t.Fatalf("unexpected video pricing: %#v", video)
	}

	kimiK3 := Lookup("kimi-k3")
	if kimiK3.Status != StatusPriced || kimiK3.Tokens == nil || kimiK3.Tokens.Input != 3 || kimiK3.Tokens.CachedInput == nil || *kimiK3.Tokens.CachedInput != 0.3 || kimiK3.Tokens.Output != 15 {
		t.Fatalf("unexpected Kimi K3 pricing: %#v", kimiK3)
	}

	missing := Lookup("model-discovered-after-snapshot")
	if missing.Status != StatusUnavailable || missing.UnavailableReason != "not_in_snapshot" {
		t.Fatalf("unexpected missing pricing: %#v", missing)
	}
}

func TestClaudeCosts(t *testing.T) {
	costs := ClaudeCosts()
	claude := costs["claude-opus-4-8"]
	if claude.InputTokens != 5 || claude.PromptCacheWriteTokens != 6.25 || claude.PromptCacheReadTokens != 0.5 || claude.PromptCacheWrite1hTokens == nil || *claude.PromptCacheWrite1hTokens != 10 {
		t.Fatalf("unexpected Claude cache projection: %#v", claude)
	}
	xai := costs["grok-4.5"]
	if xai.PromptCacheWriteTokens != 2 || xai.PromptCacheReadTokens != 0.5 || xai.WebSearchRequests != 0.005 {
		t.Fatalf("unexpected xAI cache projection: %#v", xai)
	}
	if _, ok := costs["gpt-5.3-codex-spark"]; ok {
		t.Fatal("unavailable model was projected into Claude costs")
	}
}

func TestXAIFields(t *testing.T) {
	fields := XAIFields(Lookup("gpt-5.4"))
	if fields["prompt_text_token_price"] != int64(25000) || fields["cached_prompt_text_token_price"] != int64(2500) || fields["completion_text_token_price"] != int64(150000) {
		t.Fatalf("unexpected standard tick fields: %#v", fields)
	}
	if fields["long_context_threshold"] != int64(272001) || fields["prompt_text_token_price_long_context"] != int64(50000) {
		t.Fatalf("unexpected tier tick fields: %#v", fields)
	}
}

func TestParseRejectsMalformedCatalog(t *testing.T) {
	base := Record{
		ModelID:       "test-model",
		Status:        StatusPriced,
		Currency:      CurrencyUSD,
		SnapshotDate:  SnapshotDate,
		EffectiveDate: SnapshotDate,
		SourceURL:     "https://example.com/pricing",
		Tokens:        &TokenRates{Input: 1, Output: 2},
	}
	tests := []struct {
		name    string
		mutate  func(*Record)
		wantErr string
	}{
		{"status", func(r *Record) { r.Status = "maybe" }, "invalid status"},
		{"currency", func(r *Record) { r.Currency = "EUR" }, "invalid currency"},
		{"negative", func(r *Record) { r.Tokens.Input = -1 }, "negative tokens rate"},
		{"url", func(r *Record) { r.SourceURL = "not a URL" }, "invalid source_url"},
		{"date", func(r *Record) { r.SnapshotDate = "yesterday" }, "invalid snapshot_date"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := base
			tokens := *base.Tokens
			record.Tokens = &tokens
			tt.mutate(&record)
			data, err := json.Marshal([]Record{record})
			if err != nil {
				t.Fatal(err)
			}
			_, err = Parse(data)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Parse() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseRejectsDuplicateModels(t *testing.T) {
	record := `{"model_id":"same","status":"unavailable","currency":"USD","snapshot_date":"2026-07-14","effective_date":"2026-07-14","source_url":"https://example.com","unavailable_reason":"test"}`
	_, err := Parse([]byte("[" + record + "," + record + "]"))
	if err == nil || !strings.Contains(err.Error(), "duplicate model_id") {
		t.Fatalf("Parse() error = %v, want duplicate model_id", err)
	}
}
