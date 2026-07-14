package management

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type subscriptionUsageResponse struct {
	FetchedAt string                               `json:"fetchedAt"`
	Providers map[string]subscriptionProviderUsage `json:"providers"`
}

type subscriptionProviderUsage struct {
	Mode       string                    `json:"mode"`
	FetchedAt  string                    `json:"fetchedAt"`
	Windows    []subscriptionUsageWindow `json:"windows"`
	State      string                    `json:"state"`
	CooldownAt string                    `json:"cooldownAt,omitempty"`
	Error      string                    `json:"error,omitempty"`
}

type subscriptionUsageWindow struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Used    float64 `json:"used"`
	Limit   float64 `json:"limit"`
	Percent float64 `json:"usedPercent"`
	ResetAt string  `json:"resetAt,omitempty"`
}

var subscriptionHTTPClient = &http.Client{Timeout: 8 * time.Second}

// GetSubscriptionUsage returns sanitized subscription windows and observed quota state.
func (h *Handler) GetSubscriptionUsage(c *gin.Context) {
	now := time.Now().UTC()
	requested := requestedUsageProviders(c.Query("providers"))
	result := subscriptionUsageResponse{FetchedAt: now.Format(time.RFC3339), Providers: make(map[string]subscriptionProviderUsage)}

	h.mu.Lock()
	manager := h.authManager
	h.mu.Unlock()
	if manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}
	auths := manager.List()

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, provider := range requested {
		provider := provider
		wg.Add(1)
		go func() {
			defer wg.Done()
			usage := observedProviderUsage(provider, auths, now)
			if provider == "codex" {
				if authoritative, errFetch := fetchCodexSubscriptionUsage(c.Request.Context(), auths, now); errFetch == nil {
					usage = authoritative
				} else {
					usage.Error = errFetch.Error()
				}
			} else if provider == "antigravity" {
				if authoritative, errFetch := fetchAntigravitySubscriptionUsage(c.Request.Context(), auths, now); errFetch == nil {
					usage = authoritative
				} else {
					usage.Error = errFetch.Error()
				}
			} else if provider == "kimi" {
				if authoritative, errFetch := fetchKimiSubscriptionUsage(c.Request.Context(), auths, now); errFetch == nil {
					usage = authoritative
				} else {
					usage.Error = errFetch.Error()
				}
			} else if provider == "grok" {
				if authoritative, errFetch := fetchGrokSubscriptionUsage(c.Request.Context(), auths, now); errFetch == nil {
					usage = authoritative
				} else {
					usage.Error = errFetch.Error()
				}
			}
			mu.Lock()
			result.Providers[provider] = usage
			mu.Unlock()
		}()
	}
	wg.Wait()
	c.JSON(http.StatusOK, result)
}

func fetchAntigravitySubscriptionUsage(ctx context.Context, auths []*coreauth.Auth, now time.Time) (subscriptionProviderUsage, error) {
	if local, err := fetchAntigravityLocalUsage(ctx, now); err == nil {
		return local, nil
	}
	var token, projectID string
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "antigravity") || auth.Disabled {
			continue
		}
		token, _ = auth.Metadata["access_token"].(string)
		projectID, _ = auth.Metadata["project_id"].(string)
		if strings.TrimSpace(token) != "" {
			break
		}
	}
	if strings.TrimSpace(token) == "" {
		return subscriptionProviderUsage{}, fmt.Errorf("Antigravity access token unavailable")
	}
	payload := make(map[string]string)
	if strings.TrimSpace(projectID) != "" {
		payload["project"] = projectID
	}
	body, errMarshal := json.Marshal(payload)
	if errMarshal != nil {
		return subscriptionProviderUsage{}, errMarshal
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, "https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota", strings.NewReader(string(body)))
	if errReq != nil {
		return subscriptionProviderUsage{}, errReq
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "antigravity-cli")
	resp, errDo := subscriptionHTTPClient.Do(req)
	if errDo != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("Antigravity usage request failed: %w", errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return subscriptionProviderUsage{}, fmt.Errorf("Antigravity usage returned HTTP %d", resp.StatusCode)
	}
	var response struct {
		Buckets []antigravityQuotaBucket `json:"buckets"`
	}
	if errDecode := json.NewDecoder(resp.Body).Decode(&response); errDecode != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("decode Antigravity usage: %w", errDecode)
	}
	windows := normalizeAntigravityWindows(response.Buckets, now)
	if len(windows) == 0 {
		return subscriptionProviderUsage{}, fmt.Errorf("Antigravity usage response has no quota windows")
	}
	return subscriptionProviderUsage{Mode: "authoritative", FetchedAt: now.Format(time.RFC3339), Windows: windows, State: "available"}, nil
}

var agyPortPattern = regexp.MustCompile(`127\.0\.0\.1:(\d+) \(LISTEN\)`)

func fetchAntigravityLocalUsage(ctx context.Context, now time.Time) (subscriptionProviderUsage, error) {
	if runtime.GOOS != "darwin" {
		return subscriptionProviderUsage{}, fmt.Errorf("Antigravity local quota discovery unsupported on %s", runtime.GOOS)
	}
	pidsRaw, err := exec.CommandContext(ctx, "pgrep", "-x", "agy").Output()
	if err != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("Antigravity CLI is not running")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	defer transport.CloseIdleConnections()
	for _, pid := range strings.Fields(string(pidsRaw)) {
		portsRaw, errPorts := exec.CommandContext(ctx, "lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-a", "-p", pid).Output()
		if errPorts != nil {
			continue
		}
		for _, match := range agyPortPattern.FindAllStringSubmatch(string(portsRaw), -1) {
			for _, scheme := range []string{"https", "http"} {
				url := scheme + "://127.0.0.1:" + match[1] + "/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary"
				body := `{"metadata":{"ideName":"antigravity","extensionName":"antigravity","locale":"en","ideVersion":"unknown"}}`
				req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
				if errReq != nil {
					continue
				}
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Connect-Protocol-Version", "1")
				resp, errDo := client.Do(req)
				if errDo != nil {
					continue
				}
				var summary antigravityQuotaSummaryResponse
				decodeErr := json.NewDecoder(resp.Body).Decode(&summary)
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusOK || decodeErr != nil {
					continue
				}
				windows := normalizeAntigravitySummary(summary)
				if len(windows) > 0 {
					return subscriptionProviderUsage{Mode: "authoritative", FetchedAt: now.Format(time.RFC3339), Windows: windows, State: "available"}, nil
				}
			}
		}
	}
	return subscriptionProviderUsage{}, fmt.Errorf("Antigravity local quota RPC unavailable")
}

type antigravityQuotaSummaryResponse struct {
	Response *struct {
		Groups []struct {
			DisplayName string `json:"displayName"`
			Buckets     []struct {
				BucketID          string  `json:"bucketId"`
				DisplayName       string  `json:"displayName"`
				Window            string  `json:"window"`
				RemainingFraction float64 `json:"remainingFraction"`
				ResetTime         string  `json:"resetTime"`
			} `json:"buckets"`
		} `json:"groups"`
	} `json:"response"`
}

func normalizeAntigravitySummary(summary antigravityQuotaSummaryResponse) []subscriptionUsageWindow {
	if summary.Response == nil {
		return nil
	}
	windows := make([]subscriptionUsageWindow, 0, 4)
	for _, group := range summary.Response.Groups {
		family := "Claude/GPT"
		if strings.Contains(strings.ToLower(group.DisplayName), "gemini") {
			family = "Gemini"
		}
		for _, bucket := range group.Buckets {
			remaining := bucket.RemainingFraction * 100
			used := 100 - remaining
			label := "Antigravity " + family + " " + bucket.Window
			windows = append(windows, subscriptionUsageWindow{ID: bucket.BucketID, Label: label, Used: used, Limit: 100, Percent: used, ResetAt: bucket.ResetTime})
		}
	}
	return windows
}

type antigravityQuotaBucket struct {
	RemainingFraction *float64 `json:"remainingFraction"`
	ResetTime         string   `json:"resetTime"`
	ModelID           string   `json:"modelId"`
}

func normalizeAntigravityWindows(raw []antigravityQuotaBucket, now time.Time) []subscriptionUsageWindow {
	type key struct{ family, window string }
	best := make(map[key]subscriptionUsageWindow)
	for _, bucket := range raw {
		if bucket.RemainingFraction == nil {
			continue
		}
		reset, err := time.Parse(time.RFC3339, bucket.ResetTime)
		if err != nil {
			continue
		}
		family, label := "claude-gpt", "Antigravity Claude/GPT"
		if strings.Contains(strings.ToLower(bucket.ModelID), "gemini") {
			family, label = "gemini", "Antigravity Gemini"
		}
		window := "weekly"
		if reset.Sub(now) <= 6*time.Hour {
			window = "5h"
		}
		remaining := *bucket.RemainingFraction * 100
		used := 100 - remaining
		row := subscriptionUsageWindow{ID: family + "-" + window, Label: label + " " + window, Used: used, Limit: 100, Percent: used, ResetAt: reset.UTC().Format(time.RFC3339)}
		k := key{family, window}
		if previous, ok := best[k]; !ok || row.Percent > previous.Percent {
			best[k] = row
		}
	}
	order := []key{{"gemini", "weekly"}, {"gemini", "5h"}, {"claude-gpt", "weekly"}, {"claude-gpt", "5h"}}
	windows := make([]subscriptionUsageWindow, 0, len(best))
	for _, k := range order {
		if row, ok := best[k]; ok {
			windows = append(windows, row)
		}
	}
	return windows
}

func fetchCodexSubscriptionUsage(ctx context.Context, auths []*coreauth.Auth, now time.Time) (subscriptionProviderUsage, error) {
	var token, accountID string
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") || auth.Disabled {
			continue
		}
		token, _ = auth.Metadata["access_token"].(string)
		accountID, _ = auth.Metadata["account_id"].(string)
		if strings.TrimSpace(token) != "" {
			break
		}
	}
	if strings.TrimSpace(token) == "" {
		return subscriptionProviderUsage{}, fmt.Errorf("Codex access token unavailable")
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", nil)
	if errReq != nil {
		return subscriptionProviderUsage{}, errReq
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-cli")
	if strings.TrimSpace(accountID) != "" {
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
	resp, errDo := subscriptionHTTPClient.Do(req)
	if errDo != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("Codex usage request failed: %w", errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return subscriptionProviderUsage{}, fmt.Errorf("Codex usage returned HTTP %d", resp.StatusCode)
	}
	var body map[string]any
	if errDecode := json.NewDecoder(resp.Body).Decode(&body); errDecode != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("decode Codex usage: %w", errDecode)
	}
	windows := normalizeCodexWindows(body)
	if len(windows) == 0 {
		return subscriptionProviderUsage{}, fmt.Errorf("Codex usage response has no rate-limit windows")
	}
	return subscriptionProviderUsage{Mode: "authoritative", FetchedAt: now.Format(time.RFC3339), Windows: windows, State: "available"}, nil
}

func normalizeCodexWindows(body map[string]any) []subscriptionUsageWindow {
	rateLimit, _ := body["rate_limit"].(map[string]any)
	if rateLimit == nil {
		rateLimit, _ = body["rate_limits"].(map[string]any)
	}
	if rateLimit == nil {
		return nil
	}
	var windows []subscriptionUsageWindow
	for _, candidate := range []struct {
		keys      []string
		id, label string
	}{
		{[]string{"primary_window", "primary"}, "5h", "Codex 5h"},
		{[]string{"secondary_window", "secondary"}, "weekly", "Codex weekly"},
	} {
		var raw map[string]any
		for _, key := range candidate.keys {
			if value, ok := rateLimit[key].(map[string]any); ok {
				raw = value
				break
			}
		}
		if raw == nil {
			continue
		}
		percent, ok := usageNumber(raw["used_percent"])
		if !ok {
			continue
		}
		id, label := codexWindowIdentity(raw, candidate.id, candidate.label)
		resetAt := usageResetAt(raw)
		windows = append(windows, subscriptionUsageWindow{
			ID: id, Label: label, Used: percent, Limit: 100, Percent: percent, ResetAt: resetAt,
		})
	}
	return windows
}

func codexWindowIdentity(raw map[string]any, fallbackID, fallbackLabel string) (string, string) {
	seconds, ok := usageNumber(raw["limit_window_seconds"])
	if !ok {
		return fallbackID, fallbackLabel
	}
	switch {
	case seconds >= float64(6*24*time.Hour/time.Second):
		return "weekly", "Codex weekly"
	case seconds <= float64(6*time.Hour/time.Second):
		return "5h", "Codex 5h"
	default:
		return fallbackID, fallbackLabel
	}
}

func usageResetAt(raw map[string]any) string {
	value := raw["reset_at"]
	if value == nil {
		value = raw["resets_at"]
	}
	if text := usageString(value); text != "" {
		if seconds, err := strconv.ParseInt(text, 10, 64); err == nil {
			return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
		}
		return text
	}
	if seconds, ok := usageNumber(value); ok {
		return time.Unix(int64(seconds), 0).UTC().Format(time.RFC3339)
	}
	return ""
}

func fetchGrokSubscriptionUsage(ctx context.Context, auths []*coreauth.Auth, now time.Time) (subscriptionProviderUsage, error) {
	var token string
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "xai") || auth.Disabled {
			continue
		}
		token, _ = auth.Metadata["access_token"].(string)
		if strings.TrimSpace(token) != "" {
			break
		}
	}
	if token == "" {
		return subscriptionProviderUsage{}, fmt.Errorf("Grok access token unavailable")
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, "https://cli-chat-proxy.grok.com/v1/billing?format=credits", nil)
	if errReq != nil {
		return subscriptionProviderUsage{}, errReq
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-grok-client-version", "0.2.93")
	resp, errDo := subscriptionHTTPClient.Do(req)
	if errDo != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("Grok usage request failed: %w", errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return subscriptionProviderUsage{}, fmt.Errorf("Grok usage returned HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if errDecode := json.NewDecoder(resp.Body).Decode(&payload); errDecode != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("decode Grok usage: %w", errDecode)
	}
	config, _ := payload["config"].(map[string]any)
	windows := normalizeGrokWindows(config)
	if len(windows) == 0 {
		return subscriptionProviderUsage{}, fmt.Errorf("Grok billing response has no usage windows")
	}
	return subscriptionProviderUsage{Mode: "authoritative", FetchedAt: now.Format(time.RFC3339), Windows: windows, State: "available"}, nil
}

func normalizeGrokWindows(config map[string]any) []subscriptionUsageWindow {
	var windows []subscriptionUsageWindow
	period, _ := config["currentPeriod"].(map[string]any)
	resetAt := usageString(period["end"])
	for _, candidate := range []struct {
		id, label, percent string
	}{
		{"weekly", "Grok weekly", "weeklyUsagePercent"},
		{"monthly", "Grok monthly", "monthlyUsagePercent"},
		{"monthly", "Grok monthly", "endmonthcreditUsagePercent"},
	} {
		if percent, ok := usageNumber(config[candidate.percent]); ok {
			windows = append(windows, subscriptionUsageWindow{ID: candidate.id, Label: candidate.label, Used: percent, Limit: 100, Percent: percent, ResetAt: resetAt})
		}
	}
	// Grok omits the utilization field when it is zero, while /usage show
	// still renders the declared current period as 0%.
	if len(windows) == 0 && resetAt != "" {
		periodType := strings.ToUpper(usageString(period["type"]))
		switch {
		case strings.Contains(periodType, "WEEKLY"):
			windows = append(windows, subscriptionUsageWindow{ID: "weekly", Label: "Grok weekly", Limit: 100, ResetAt: resetAt})
		case strings.Contains(periodType, "MONTHLY"):
			windows = append(windows, subscriptionUsageWindow{ID: "monthly", Label: "Grok monthly", Limit: 100, ResetAt: resetAt})
		}
	}
	return windows
}

func requestedUsageProviders(raw string) []string {
	allowed := map[string]bool{"codex": true, "grok": true, "antigravity": true, "kimi": true}
	if strings.TrimSpace(raw) == "" {
		return []string{"codex", "grok", "antigravity", "kimi"}
	}
	seen := make(map[string]bool)
	out := make([]string, 0, 4)
	for _, value := range strings.Split(raw, ",") {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "agy" {
			value = "antigravity"
		}
		if allowed[value] && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

func observedProviderUsage(provider string, auths []*coreauth.Auth, now time.Time) subscriptionProviderUsage {
	lookup := provider
	if provider == "grok" {
		lookup = "xai"
	}
	usage := subscriptionProviderUsage{Mode: "observed", FetchedAt: now.Format(time.RFC3339), Windows: []subscriptionUsageWindow{}, State: "unavailable"}
	found := false
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), lookup) {
			continue
		}
		found = true
		if !auth.Disabled && !auth.Unavailable && !auth.Quota.Exceeded {
			usage.State = "available"
		}
		if auth.Quota.Exceeded || auth.Unavailable {
			usage.State = "exhausted"
			recoverAt := auth.Quota.NextRecoverAt
			if recoverAt.IsZero() {
				recoverAt = auth.NextRetryAfter
			}
			if !recoverAt.IsZero() && (usage.CooldownAt == "" || recoverAt.Before(parseUsageTime(usage.CooldownAt))) {
				usage.CooldownAt = recoverAt.UTC().Format(time.RFC3339)
			}
		}
	}
	if !found {
		usage.Error = "no configured credential"
	}
	return usage
}

func fetchKimiSubscriptionUsage(ctx context.Context, auths []*coreauth.Auth, now time.Time) (subscriptionProviderUsage, error) {
	var token string
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "kimi") || auth.Disabled {
			continue
		}
		token, _ = auth.Metadata["access_token"].(string)
		if strings.TrimSpace(token) != "" {
			break
		}
	}
	if token == "" {
		return subscriptionProviderUsage{}, fmt.Errorf("Kimi access token unavailable")
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.kimi.com/coding/v1/usages", nil)
	if errReq != nil {
		return subscriptionProviderUsage{}, errReq
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, errDo := subscriptionHTTPClient.Do(req)
	if errDo != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("Kimi usage request failed: %w", errDo)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return subscriptionProviderUsage{}, fmt.Errorf("Kimi usage returned HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if errDecode := json.NewDecoder(resp.Body).Decode(&payload); errDecode != nil {
		return subscriptionProviderUsage{}, fmt.Errorf("decode Kimi usage: %w", errDecode)
	}
	windows := make([]subscriptionUsageWindow, 0, 4)
	if row, ok := normalizeKimiWindow("weekly", "Kimi weekly", payload["usage"]); ok {
		windows = append(windows, row)
	}
	if limits, ok := payload["limits"].([]any); ok {
		for index, raw := range limits {
			item, _ := raw.(map[string]any)
			detail := item
			if nested, okNested := item["detail"].(map[string]any); okNested {
				detail = nested
			}
			label := fmt.Sprintf("Kimi limit %d", index+1)
			if window, okWindow := item["window"].(map[string]any); okWindow {
				label = kimiWindowLabel(window, label)
			}
			if row, okRow := normalizeKimiWindow(fmt.Sprintf("limit-%d", index+1), label, detail); okRow {
				windows = append(windows, row)
			}
		}
	}
	return subscriptionProviderUsage{Mode: "authoritative", FetchedAt: now.Format(time.RFC3339), Windows: windows, State: "available"}, nil
}

func normalizeKimiWindow(id, label string, raw any) (subscriptionUsageWindow, bool) {
	value, ok := raw.(map[string]any)
	if !ok {
		return subscriptionUsageWindow{}, false
	}
	limit, okLimit := usageNumber(value["limit"])
	used, okUsed := usageNumber(value["used"])
	if !okUsed {
		if remaining, okRemaining := usageNumber(value["remaining"]); okRemaining && okLimit {
			used = limit - remaining
			okUsed = true
		}
	}
	if !okLimit || !okUsed || limit <= 0 {
		return subscriptionUsageWindow{}, false
	}
	resetAt := usageString(value["resetTime"])
	if resetAt == "" {
		resetAt = usageString(value["reset_at"])
	}
	return subscriptionUsageWindow{ID: id, Label: label, Used: used, Limit: limit, Percent: used / limit * 100, ResetAt: resetAt}, true
}

func kimiWindowLabel(window map[string]any, fallback string) string {
	duration, ok := usageNumber(window["duration"])
	unit := strings.ToUpper(usageString(window["timeUnit"]))
	if !ok {
		return fallback
	}
	if strings.Contains(unit, "MINUTE") && int(duration)%60 == 0 {
		return fmt.Sprintf("Kimi %dh", int(duration)/60)
	}
	return fmt.Sprintf("Kimi %d%s", int(duration), strings.ToLower(strings.TrimSuffix(unit, "S")))
}

func usageNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		parsed, errParse := strconv.ParseFloat(typed, 64)
		return parsed, errParse == nil
	default:
		return 0, false
	}
}

func usageString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func parseUsageTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}
