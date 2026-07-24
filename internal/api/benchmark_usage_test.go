package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBenchmarkUsageRouteRequiresAPIAuthentication(t *testing.T) {
	server := newTestServerWithOptions(t)
	path := "/v1/benchmark/usage/123e4567-e89b-42d3-a456-426614174000"

	unauthorized := httptest.NewRecorder()
	server.engine.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, path, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, body = %s", unauthorized.Code, unauthorized.Body.String())
	}

	authorizedRequest := httptest.NewRequest(http.MethodGet, path, nil)
	authorizedRequest.Header.Set("Authorization", "Bearer test-key")
	authorized := httptest.NewRecorder()
	server.engine.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusNotFound {
		t.Fatalf("authorized status = %d, body = %s", authorized.Code, authorized.Body.String())
	}
}
