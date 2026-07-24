package benchmarkusage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const (
	// HeaderName carries an opaque benchmark request identifier from claude-all.
	HeaderName = "X-Claude-All-Benchmark-ID"
	storeTTL   = 10 * time.Minute
	storeLimit = 2048
)

var benchmarkIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type contextKey struct{}

type requestMetadata struct {
	benchmarkID string
	principal   string
}

// TokenDetail contains only numeric usage counters safe for benchmark output.
type TokenDetail struct {
	InputTokens         int64 `json:"input_tokens"`
	OutputTokens        int64 `json:"output_tokens"`
	ReasoningTokens     int64 `json:"reasoning_tokens"`
	CachedTokens        int64 `json:"cached_tokens"`
	CacheReadTokens     int64 `json:"cache_read_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
}

// Record is the sanitized usage shape exposed to benchmark clients.
type Record struct {
	Provider            string      `json:"provider"`
	ExecutorType        string      `json:"executor_type"`
	Model               string      `json:"model"`
	Alias               string      `json:"alias"`
	ReasoningEffort     string      `json:"reasoning_effort"`
	ServiceTier         string      `json:"service_tier"`
	ResponseServiceTier string      `json:"response_service_tier,omitempty"`
	RequestedAt         time.Time   `json:"requested_at"`
	LatencyMS           int64       `json:"latency_ms"`
	TTFTMS              int64       `json:"ttft_ms"`
	Generate            bool        `json:"generate"`
	Failed              bool        `json:"failed"`
	StatusCode          int         `json:"status_code"`
	Tokens              TokenDetail `json:"tokens"`
}

// Response is the versioned benchmark usage endpoint envelope.
type Response struct {
	SchemaVersion int      `json:"schema_version"`
	BenchmarkID   string   `json:"benchmark_id"`
	Records       []Record `json:"records"`
}

type bucket struct {
	updated time.Time
	records []Record
}

// Store retains sanitized benchmark records for a short bounded window.
type Store struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	limit   int
	buckets map[string]*bucket
}

// NewStore constructs an isolated benchmark record store.
func NewStore(ttl time.Duration, limit int) *Store {
	if ttl <= 0 {
		ttl = storeTTL
	}
	if limit <= 0 {
		limit = storeLimit
	}
	return &Store{now: time.Now, ttl: ttl, limit: limit, buckets: make(map[string]*bucket)}
}

var defaultStore = NewStore(storeTTL, storeLimit)

func init() {
	coreusage.RegisterNamedPlugin("claude-all-benchmark", usagePlugin{store: defaultStore})
}

// ExtractHeader validates and removes the benchmark header before request logging and forwarding.
func ExtractHeader() gin.HandlerFunc {
	return func(c *gin.Context) {
		value := strings.TrimSpace(c.GetHeader(HeaderName))
		c.Request.Header.Del(HeaderName)
		if value == "" {
			c.Next()
			return
		}
		if !benchmarkIDPattern.MatchString(value) {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid benchmark ID"})
			return
		}
		metadata := requestMetadata{benchmarkID: strings.ToLower(value)}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), contextKey{}, metadata))
		c.Next()
	}
}

// AttachPrincipal adds the authenticated client identity to benchmark request context.
func AttachPrincipal() gin.HandlerFunc {
	return func(c *gin.Context) {
		metadata, ok := metadataFromContext(c.Request.Context())
		if ok {
			metadata.principal = c.GetString("userApiKey")
			c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), contextKey{}, metadata))
		}
		c.Next()
	}
}

// GetUsage returns sanitized records for one authenticated benchmark request.
func GetUsage(c *gin.Context) {
	getUsage(defaultStore)(c)
}

func getUsage(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		benchmarkID := strings.ToLower(strings.TrimSpace(c.Param("benchmark_id")))
		if !benchmarkIDPattern.MatchString(benchmarkID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid benchmark ID"})
			return
		}
		records, ok := store.Records(c.GetString("userApiKey"), benchmarkID)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "benchmark usage not found"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, Response{SchemaVersion: 1, BenchmarkID: benchmarkID, Records: records})
	}
}

type usagePlugin struct {
	store *Store
}

func (p usagePlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	metadata, ok := metadataFromContext(ctx)
	if !ok || p.store == nil {
		return
	}
	p.store.Add(metadata.principal, metadata.benchmarkID, sanitizeRecord(record))
}

func metadataFromContext(ctx context.Context) (requestMetadata, bool) {
	if ctx == nil {
		return requestMetadata{}, false
	}
	if metadata, ok := ctx.Value(contextKey{}).(requestMetadata); ok && metadata.benchmarkID != "" {
		return metadata, true
	}
	if ginContext, ok := ctx.Value("gin").(*gin.Context); ok && ginContext != nil && ginContext.Request != nil {
		metadata, metadataOK := ginContext.Request.Context().Value(contextKey{}).(requestMetadata)
		return metadata, metadataOK && metadata.benchmarkID != ""
	}
	return requestMetadata{}, false
}

func sanitizeRecord(record coreusage.Record) Record {
	totalTokens := record.Detail.TotalTokens
	if totalTokens == 0 {
		totalTokens = record.Detail.InputTokens + record.Detail.OutputTokens + record.Detail.ReasoningTokens
	}
	statusCode := record.Fail.StatusCode
	if !record.Failed && statusCode == 0 {
		statusCode = http.StatusOK
	}
	return Record{
		Provider:            strings.TrimSpace(record.Provider),
		ExecutorType:        strings.TrimSpace(record.ExecutorType),
		Model:               strings.TrimSpace(record.Model),
		Alias:               strings.TrimSpace(record.Alias),
		ReasoningEffort:     strings.TrimSpace(record.ReasoningEffort),
		ServiceTier:         strings.TrimSpace(record.ServiceTier),
		ResponseServiceTier: strings.TrimSpace(record.ResponseServiceTier),
		RequestedAt:         record.RequestedAt,
		LatencyMS:           record.Latency.Milliseconds(),
		TTFTMS:              record.TTFT.Milliseconds(),
		Generate:            coreusage.GenerateEnabled(record.Generate),
		Failed:              record.Failed,
		StatusCode:          statusCode,
		Tokens: TokenDetail{
			InputTokens:         record.Detail.InputTokens,
			OutputTokens:        record.Detail.OutputTokens,
			ReasoningTokens:     record.Detail.ReasoningTokens,
			CachedTokens:        record.Detail.CachedTokens,
			CacheReadTokens:     record.Detail.CacheReadTokens,
			CacheCreationTokens: record.Detail.CacheCreationTokens,
			TotalTokens:         totalTokens,
		},
	}
}

// Add stores one sanitized record under a client-scoped benchmark ID.
func (s *Store) Add(principal, benchmarkID string, record Record) {
	if s == nil || !benchmarkIDPattern.MatchString(benchmarkID) {
		return
	}
	now := s.now()
	key := scopedKey(principal, benchmarkID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(now)
	entry := s.buckets[key]
	if entry == nil {
		entry = &bucket{}
		s.buckets[key] = entry
	}
	entry.updated = now
	entry.records = append(entry.records, record)
	s.enforceLimitLocked()
}

// Records returns a copy of records for a client-scoped benchmark ID.
func (s *Store) Records(principal, benchmarkID string) ([]Record, bool) {
	if s == nil {
		return nil, false
	}
	now := s.now()
	key := scopedKey(principal, benchmarkID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(now)
	entry := s.buckets[key]
	if entry == nil || len(entry.records) == 0 {
		return nil, false
	}
	return append([]Record(nil), entry.records...), true
}

func (s *Store) purgeLocked(now time.Time) {
	for key, entry := range s.buckets {
		if now.Sub(entry.updated) >= s.ttl {
			delete(s.buckets, key)
		}
	}
}

func (s *Store) enforceLimitLocked() {
	total := 0
	type candidate struct {
		key     string
		updated time.Time
		count   int
	}
	candidates := make([]candidate, 0, len(s.buckets))
	for key, entry := range s.buckets {
		count := len(entry.records)
		total += count
		candidates = append(candidates, candidate{key: key, updated: entry.updated, count: count})
	}
	if total <= s.limit {
		return
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].updated.Before(candidates[j].updated) })
	for _, item := range candidates {
		delete(s.buckets, item.key)
		total -= item.count
		if total <= s.limit {
			return
		}
	}
}

func scopedKey(principal, benchmarkID string) string {
	digest := sha256.Sum256([]byte(principal))
	return hex.EncodeToString(digest[:]) + ":" + strings.ToLower(benchmarkID)
}
