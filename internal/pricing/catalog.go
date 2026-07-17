// Package pricing provides the curated, embedded public list-price snapshot.
// It is intentionally independent from the remotely refreshed model registry.
package pricing

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	StatusPriced      = "priced"
	StatusUnavailable = "unavailable"
	CurrencyUSD       = "USD"
	SnapshotDate      = "2026-07-16"
)

// TokenRates are public USD rates per one million tokens.
type TokenRates struct {
	Input        float64     `json:"input_per_million"`
	CachedInput  *float64    `json:"cached_input_per_million,omitempty"`
	Output       float64     `json:"output_per_million"`
	CacheWrite   *float64    `json:"cache_write_per_million,omitempty"`
	CacheWrite1h *float64    `json:"cache_write_1h_per_million,omitempty"`
	Tiers        []TokenTier `json:"tiers,omitempty"`
}

// TokenTier represents a rate change applied at or above an input-token count.
type TokenTier struct {
	MinInputTokens int64    `json:"min_input_tokens"`
	Input          float64  `json:"input_per_million"`
	CachedInput    *float64 `json:"cached_input_per_million,omitempty"`
	Output         float64  `json:"output_per_million"`
}

// ImageRate is a directly published per-image rate.
type ImageRate struct {
	Quality    string  `json:"quality"`
	Resolution string  `json:"resolution"`
	USD        float64 `json:"usd_per_image"`
}

// VideoRate is a directly published per-second video rate.
type VideoRate struct {
	Resolution string  `json:"resolution"`
	USD        float64 `json:"usd_per_second"`
}

// ToolRates contains provider-hosted tool invocation rates.
type ToolRates struct {
	WebSearch *float64 `json:"web_search_per_request,omitempty"`
}

// Record is the normalized public list-price metadata for one exact model ID.
type Record struct {
	ModelID           string      `json:"model_id,omitempty"`
	Status            string      `json:"status"`
	Currency          string      `json:"currency"`
	SnapshotDate      string      `json:"snapshot_date"`
	EffectiveDate     string      `json:"effective_date"`
	SourceURL         string      `json:"source_url"`
	UnavailableReason string      `json:"unavailable_reason,omitempty"`
	Tokens            *TokenRates `json:"tokens,omitempty"`
	ImageTokens       *TokenRates `json:"image_tokens,omitempty"`
	Images            []ImageRate `json:"images,omitempty"`
	Videos            []VideoRate `json:"videos,omitempty"`
	Tools             *ToolRates  `json:"tools,omitempty"`
}

// ClaudeCost is Claude Code's additional_model_costs wire contract. Values are
// USD per million tokens, except web_search_requests which is USD per request.
type ClaudeCost struct {
	InputTokens              float64  `json:"input_tokens"`
	OutputTokens             float64  `json:"output_tokens"`
	PromptCacheWriteTokens   float64  `json:"prompt_cache_write_tokens"`
	PromptCacheWrite1hTokens *float64 `json:"prompt_cache_write_1h_tokens,omitempty"`
	PromptCacheReadTokens    float64  `json:"prompt_cache_read_tokens"`
	WebSearchRequests        float64  `json:"web_search_requests"`
}

//go:embed catalog.json
var catalogJSON []byte

var defaultCatalog = mustLoadCatalog(catalogJSON)

func mustLoadCatalog(data []byte) map[string]Record {
	catalog, err := Parse(data)
	if err != nil {
		panic(fmt.Sprintf("pricing: invalid embedded catalog: %v", err))
	}
	return catalog
}

// Parse validates and returns a catalog keyed by exact model ID.
func Parse(data []byte) (map[string]Record, error) {
	var records []Record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode catalog: trailing JSON value")
		}
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("catalog is empty")
	}

	result := make(map[string]Record, len(records))
	for i, record := range records {
		if err := validateRecord(record); err != nil {
			return nil, fmt.Errorf("record %d (%q): %w", i, record.ModelID, err)
		}
		if _, exists := result[record.ModelID]; exists {
			return nil, fmt.Errorf("duplicate model_id %q", record.ModelID)
		}
		result[record.ModelID] = record
	}
	return result, nil
}

func validateRecord(record Record) error {
	if strings.TrimSpace(record.ModelID) == "" {
		return fmt.Errorf("model_id is required")
	}
	if record.Status != StatusPriced && record.Status != StatusUnavailable {
		return fmt.Errorf("invalid status %q", record.Status)
	}
	if record.Currency != CurrencyUSD {
		return fmt.Errorf("invalid currency %q", record.Currency)
	}
	for name, value := range map[string]string{"snapshot_date": record.SnapshotDate, "effective_date": record.EffectiveDate} {
		if _, err := time.Parse(time.DateOnly, value); err != nil {
			return fmt.Errorf("invalid %s %q", name, value)
		}
	}
	parsedURL, err := url.ParseRequestURI(record.SourceURL)
	if err != nil || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		return fmt.Errorf("invalid source_url %q", record.SourceURL)
	}
	if record.Status == StatusUnavailable {
		if strings.TrimSpace(record.UnavailableReason) == "" {
			return fmt.Errorf("unavailable_reason is required")
		}
		if record.Tokens != nil || record.ImageTokens != nil || len(record.Images) > 0 || len(record.Videos) > 0 || record.Tools != nil {
			return fmt.Errorf("unavailable record must not contain rates")
		}
		return nil
	}
	if record.UnavailableReason != "" {
		return fmt.Errorf("priced record must not contain unavailable_reason")
	}
	if record.Tokens == nil && record.ImageTokens == nil && len(record.Images) == 0 && len(record.Videos) == 0 {
		return fmt.Errorf("priced record has no rates")
	}
	if err := validateTokenRates("tokens", record.Tokens); err != nil {
		return err
	}
	if err := validateTokenRates("image_tokens", record.ImageTokens); err != nil {
		return err
	}
	for _, image := range record.Images {
		if image.Quality == "" || image.Resolution == "" || image.USD < 0 {
			return fmt.Errorf("invalid image rate")
		}
	}
	for _, video := range record.Videos {
		if video.Resolution == "" || video.USD < 0 {
			return fmt.Errorf("invalid video rate")
		}
	}
	if record.Tools != nil && record.Tools.WebSearch != nil && *record.Tools.WebSearch < 0 {
		return fmt.Errorf("negative web search rate")
	}
	return nil
}

func validateTokenRates(name string, rates *TokenRates) error {
	if rates == nil {
		return nil
	}
	if rates.Input < 0 || rates.Output < 0 || negative(rates.CachedInput) || negative(rates.CacheWrite) || negative(rates.CacheWrite1h) {
		return fmt.Errorf("negative %s rate", name)
	}
	var previous int64
	for i, tier := range rates.Tiers {
		if tier.MinInputTokens <= 0 || (i > 0 && tier.MinInputTokens <= previous) {
			return fmt.Errorf("%s tiers must have increasing positive thresholds", name)
		}
		if tier.Input < 0 || tier.Output < 0 || negative(tier.CachedInput) {
			return fmt.Errorf("negative %s tier rate", name)
		}
		previous = tier.MinInputTokens
	}
	return nil
}

func negative(value *float64) bool { return value != nil && *value < 0 }

// Lookup returns pricing for an exact model ID. Models discovered after the
// snapshot receive an explicit unavailable record instead of an inferred rate.
func Lookup(modelID string) Record {
	if record, ok := defaultCatalog[modelID]; ok {
		return record
	}
	return Record{
		Status:            StatusUnavailable,
		Currency:          CurrencyUSD,
		SnapshotDate:      SnapshotDate,
		EffectiveDate:     SnapshotDate,
		SourceURL:         "https://github.com/router-for-me/CLIProxyAPI",
		UnavailableReason: "not_in_snapshot",
	}
}

// ModelIDs returns the sorted exact IDs in the embedded snapshot.
func ModelIDs() []string {
	ids := make([]string, 0, len(defaultCatalog))
	for id := range defaultCatalog {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ClaudeCosts projects complete text-token records into Claude Code's native contract.
func ClaudeCosts() map[string]ClaudeCost {
	result := make(map[string]ClaudeCost)
	for id, record := range defaultCatalog {
		if record.Status != StatusPriced || record.Tokens == nil {
			continue
		}
		rates := record.Tokens
		cacheRead := rates.Input
		if rates.CachedInput != nil {
			cacheRead = *rates.CachedInput
		}
		cacheWrite := rates.Input
		if rates.CacheWrite != nil {
			cacheWrite = *rates.CacheWrite
		}
		webSearch := 0.0
		if record.Tools != nil && record.Tools.WebSearch != nil {
			webSearch = *record.Tools.WebSearch
		}
		result[id] = ClaudeCost{
			InputTokens:              rates.Input,
			OutputTokens:             rates.Output,
			PromptCacheWriteTokens:   cacheWrite,
			PromptCacheWrite1hTokens: rates.CacheWrite1h,
			PromptCacheReadTokens:    cacheRead,
			WebSearchRequests:        webSearch,
		}
	}
	return result
}

// XAIFields returns fields from xAI's /v1/models contract when the normalized
// rates can be represented exactly. It never creates usage.cost_in_usd_ticks.
func XAIFields(record Record) map[string]any {
	fields := map[string]any{}
	if record.Status != StatusPriced {
		return fields
	}
	if record.Tokens != nil {
		fields["prompt_text_token_price"] = tokenTicks(record.Tokens.Input)
		fields["completion_text_token_price"] = tokenTicks(record.Tokens.Output)
		if record.Tokens.CachedInput != nil {
			fields["cached_prompt_text_token_price"] = tokenTicks(*record.Tokens.CachedInput)
		}
		if len(record.Tokens.Tiers) > 0 {
			tier := record.Tokens.Tiers[0]
			fields["long_context_threshold"] = tier.MinInputTokens
			fields["prompt_text_token_price_long_context"] = tokenTicks(tier.Input)
			fields["completion_text_token_price_long_context"] = tokenTicks(tier.Output)
			if tier.CachedInput != nil {
				fields["cached_prompt_text_token_price_long_context"] = tokenTicks(*tier.CachedInput)
			}
		}
	}
	if record.ImageTokens != nil {
		fields["prompt_image_token_price"] = tokenTicks(record.ImageTokens.Input)
	}
	if len(record.Images) > 0 && allImageOutputsSamePrice(record.Images) {
		fields["image_price"] = usdTicks(record.Images[0].USD)
	}
	if record.Tools != nil && record.Tools.WebSearch != nil {
		fields["search_price"] = usdTicks(*record.Tools.WebSearch)
	}
	return fields
}

func allImageOutputsSamePrice(rates []ImageRate) bool {
	for _, rate := range rates[1:] {
		if rate.USD != rates[0].USD {
			return false
		}
	}
	return true
}

func tokenTicks(usdPerMillion float64) int64 { return int64(usdPerMillion*10000 + 0.5) }
func usdTicks(usd float64) int64             { return int64(usd*1e10 + 0.5) }
