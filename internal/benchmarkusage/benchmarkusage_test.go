package benchmarkusage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const testBenchmarkID = "123e4567-e89b-42d3-a456-426614174000"

func TestExtractHeaderStripsAndAttachesBenchmarkMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ExtractHeader())
	router.Use(func(c *gin.Context) {
		c.Set("userApiKey", "client-secret")
		c.Next()
	})
	router.Use(AttachPrincipal())
	router.POST("/v1/messages", func(c *gin.Context) {
		metadata, ok := metadataFromContext(c.Request.Context())
		if !ok {
			t.Fatal("benchmark metadata missing")
		}
		if metadata.benchmarkID != testBenchmarkID || metadata.principal != "client-secret" {
			t.Fatalf("metadata = %#v", metadata)
		}
		if value := c.GetHeader(HeaderName); value != "" {
			t.Fatalf("benchmark header was not stripped: %q", value)
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set(HeaderName, testBenchmarkID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestExtractHeaderRejectsInvalidBenchmarkID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ExtractHeader())
	router.GET("/v1/messages", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	req.Header.Set(HeaderName, "not-a-uuid")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestUsagePluginStoresSanitizedClientScopedRecords(t *testing.T) {
	store := NewStore(time.Minute, 10)
	plugin := usagePlugin{store: store}
	ctx := context.WithValue(context.Background(), contextKey{}, requestMetadata{
		benchmarkID: testBenchmarkID,
		principal:   "client-secret",
	})
	plugin.HandleUsage(ctx, coreusage.Record{
		Provider:            " codex ",
		ExecutorType:        "responses",
		Model:               "gpt-5.6",
		Alias:               "gpt-5.6-sol",
		APIKey:              "must-not-leak",
		AuthID:              "must-not-leak",
		ReasoningEffort:     "high",
		ServiceTier:         "auto",
		ResponseServiceTier: "priority",
		RequestedAt:         time.Unix(100, 0).UTC(),
		Latency:             3 * time.Second,
		TTFT:                400 * time.Millisecond,
		Generate:            coreusage.GenerateFlag(true),
		Detail: coreusage.Detail{
			InputTokens:     10,
			OutputTokens:    20,
			ReasoningTokens: 5,
		},
	})

	records, ok := store.Records("client-secret", testBenchmarkID)
	if !ok || len(records) != 1 {
		t.Fatalf("records = %#v, ok = %v", records, ok)
	}
	record := records[0]
	if record.Provider != "codex" || record.LatencyMS != 3000 || record.TTFTMS != 400 || record.Tokens.TotalTokens != 35 {
		t.Fatalf("record = %#v", record)
	}
	if _, okWrong := store.Records("different-client", testBenchmarkID); okWrong {
		t.Fatal("record was visible to another principal")
	}
	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"must-not-leak", "api_key", "auth_id", "response_headers", "body"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("payload contains %q: %s", forbidden, payload)
		}
	}
}

func TestUsagePluginRecoversMetadataFromEmbeddedGinContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewStore(time.Minute, 10)
	plugin := usagePlugin{store: store}
	request := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	request = request.WithContext(context.WithValue(request.Context(), contextKey{}, requestMetadata{
		benchmarkID: testBenchmarkID,
		principal:   "client-secret",
	}))
	ginContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginContext.Request = request
	executorContext := context.WithValue(context.Background(), "gin", ginContext)

	plugin.HandleUsage(executorContext, coreusage.Record{
		Model:  "model",
		Detail: coreusage.Detail{OutputTokens: 10},
	})
	if records, ok := store.Records("client-secret", testBenchmarkID); !ok || len(records) != 1 {
		t.Fatalf("records = %#v, ok = %v", records, ok)
	}
}

func TestStoreExpiresAndEvictsOldestBuckets(t *testing.T) {
	now := time.Unix(100, 0)
	store := NewStore(time.Minute, 2)
	store.now = func() time.Time { return now }
	ids := []string{
		"123e4567-e89b-42d3-a456-426614174001",
		"123e4567-e89b-42d3-a456-426614174002",
		"123e4567-e89b-42d3-a456-426614174003",
	}
	for _, id := range ids {
		store.Add("client", id, Record{Model: id})
		now = now.Add(time.Second)
	}
	if _, ok := store.Records("client", ids[0]); ok {
		t.Fatal("oldest bucket was not evicted")
	}
	if _, ok := store.Records("client", ids[2]); !ok {
		t.Fatal("newest bucket was evicted")
	}
	now = now.Add(time.Minute)
	if _, ok := store.Records("client", ids[2]); ok {
		t.Fatal("expired bucket was returned")
	}
}

func TestGetUsageReturnsVersionedNoStoreEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := NewStore(time.Minute, 10)
	store.Add("client", testBenchmarkID, Record{Model: "model"})
	router := gin.New()
	router.GET("/v1/benchmark/usage/:benchmark_id", func(c *gin.Context) {
		c.Set("userApiKey", "client")
		getUsage(store)(c)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/benchmark/usage/"+testBenchmarkID, nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status = %d, cache = %q, body = %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var envelope Response
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.BenchmarkID != testBenchmarkID || len(envelope.Records) != 1 {
		t.Fatalf("envelope = %#v", envelope)
	}
}
