package executor

import "testing"

func TestXAIStreamDiagnosticsCompletedWithUsage(t *testing.T) {
	diagnostics := xaiStreamDiagnostics{model: "grok-4.5"}
	diagnostics.observeCompleted([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":21,"output_tokens":8,"input_tokens_details":{"cached_tokens":5}}}}`))
	diagnostics.observeTranslatedChunk([]byte(`data: {"type":"message_delta","usage":{"input_tokens":16,"output_tokens":8}}`))
	diagnostics.observeTranslatedChunk([]byte(`data: {"type":"message_stop"}`))

	if got := diagnostics.outcome(); got != "completed_with_usage" {
		t.Fatalf("outcome = %q, want completed_with_usage", got)
	}
	if diagnostics.inputTokens != 21 || diagnostics.outputTokens != 8 || diagnostics.cachedTokens != 5 {
		t.Fatalf("tokens = (%d, %d, %d), want (21, 8, 5)", diagnostics.inputTokens, diagnostics.outputTokens, diagnostics.cachedTokens)
	}
}

func TestXAIStreamDiagnosticsFailureOutcomes(t *testing.T) {
	tests := []struct {
		name        string
		diagnostics xaiStreamDiagnostics
		want        string
	}{
		{
			name: "completed without usage",
			diagnostics: func() xaiStreamDiagnostics {
				d := xaiStreamDiagnostics{}
				d.observeCompleted([]byte(`{"type":"response.completed","response":{}}`))
				return d
			}(),
			want: "completed_without_usage",
		},
		{
			name:        "translation incomplete",
			diagnostics: xaiStreamDiagnostics{responseCompleted: true, usagePresent: true, translatedMessageDelta: true},
			want:        "completed_translation_incomplete",
		},
		{
			name:        "client canceled during terminal translation",
			diagnostics: xaiStreamDiagnostics{responseCompleted: true, usagePresent: true, translatedMessageDelta: true, contextCanceled: true},
			want:        "completed_downstream_canceled",
		},
		{
			name:        "client canceled",
			diagnostics: xaiStreamDiagnostics{contextCanceled: true},
			want:        "canceled_before_completed",
		},
		{
			name:        "upstream scan error",
			diagnostics: xaiStreamDiagnostics{scannerError: true},
			want:        "scan_error_before_completed",
		},
		{
			name:        "upstream disconnected",
			diagnostics: xaiStreamDiagnostics{},
			want:        "disconnected_before_completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.diagnostics.outcome(); got != tt.want {
				t.Fatalf("outcome = %q, want %q", got, tt.want)
			}
		})
	}
}
