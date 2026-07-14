package claude

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/tidwall/gjson"
)

func TestSortClaudeModelsByDisplayName(t *testing.T) {
	models := []map[string]any{
		{"id": "zebra-model", "display_name": "Zebra"},
		{"id": "claude-a", "display_name": "Alpha"},
		{"id": "claude-c", "display_name": "Alpha"},
		{"id": "beta-model", "display_name": "Beta"},
	}
	sortClaudeModelsByDisplayName(models)

	wantIDs := []string{"claude-a", "claude-c", "beta-model", "zebra-model"}
	for i, want := range wantIDs {
		got, _ := models[i]["id"].(string)
		if got != want {
			t.Fatalf("models[%d].id = %q, want %q", i, got, want)
		}
	}
}

func TestClaudeModelsResponseUsesConfiguredDisplayName(t *testing.T) {
	const clientID = "claude-display-name-catalog-test"
	const modelID = "claude-display-name-catalog-test"
	registryRef := registry.GetGlobalRegistry()
	registryRef.RegisterClient(clientID, "claude", []*registry.ModelInfo{{
		ID: modelID, Object: "model", OwnedBy: "test", DisplayName: "Configured Claude Name",
	}})
	t.Cleanup(func() {
		registryRef.UnregisterClient(clientID)
	})

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	NewClaudeCodeAPIHandler(&handlers.BaseAPIHandler{}).ClaudeModels(ctx)

	var response struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if errUnmarshal := json.Unmarshal(recorder.Body.Bytes(), &response); errUnmarshal != nil {
		t.Fatalf("decode response: %v", errUnmarshal)
	}
	for _, model := range response.Data {
		if model.ID == modelID {
			if model.DisplayName != "Configured Claude Name" {
				t.Fatalf("display_name = %q, want Configured Claude Name", model.DisplayName)
			}
			return
		}
	}
	t.Fatalf("model %q not found in response", modelID)
}

func TestRewriteClaudeDDModelInBody(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantModel string
	}{
		{
			name:      "encoded model is decoded",
			body:      `{"model":"claude-fable-5-dd-o4-tpg","messages":[]}`,
			wantModel: "gpt-4o",
		},
		{
			name:      "plain claude model unchanged",
			body:      `{"model":"claude-sonnet-4-6","messages":[]}`,
			wantModel: "claude-sonnet-4-6",
		},
		{
			name:      "encoded model with thinking suffix",
			body:      `{"model":"claude-fable-5-dd-o4-tpg(high)","stream":true}`,
			wantModel: "gpt-4o(high)",
		},
		{
			name:      "missing model field unchanged",
			body:      `{"messages":[]}`,
			wantModel: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewriteClaudeDDModelInBody([]byte(tt.body))
			if model := gjson.GetBytes(got, "model").String(); model != tt.wantModel {
				t.Fatalf("model = %q, want %q; body=%s", model, tt.wantModel, string(got))
			}
		})
	}
}

func TestClaudeResponseModel(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "legacy encoded model is retained for the response",
			body: `{"model":"claude-fable-5-dd-tnega-hsalf-3-inimeg","messages":[]}`,
			want: "claude-fable-5-dd-tnega-hsalf-3-inimeg",
		},
		{
			name: "real external model is retained for the response",
			body: `{"model":"gemini-3-flash-agent","messages":[]}`,
			want: "gemini-3-flash-agent",
		},
		{
			name: "native Claude model is retained for the response",
			body: `{"model":"claude-sonnet-4-6","messages":[]}`,
			want: "claude-sonnet-4-6",
		},
		{
			name: "missing model needs no rewrite",
			body: `{"messages":[]}`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := claudeResponseModel([]byte(tt.body)); got != tt.want {
				t.Fatalf("response model = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteClaudeResponseModel(t *testing.T) {
	const clientModel = "gemini-3-flash-agent"
	response := []byte(`{"id":"msg_1","type":"message","model":"gemini-3-flash-a","content":[]}`)

	got := rewriteClaudeResponseModel(response, clientModel)
	if model := gjson.GetBytes(got, "model").String(); model != clientModel {
		t.Fatalf("model = %q, want %q; response=%s", model, clientModel, string(got))
	}
}
