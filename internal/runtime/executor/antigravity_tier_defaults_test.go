package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestAntigravityExecutorExecuteDefaultsTierThinkingWhenOmitted(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		source      sdktranslator.Format
		payload     string
		wantLevel   string
		wantInclude bool
	}{
		{
			name:        "gemini-4-hydrogen-xhigh openai request",
			model:       "gemini-4-hydrogen-xhigh",
			source:      sdktranslator.FormatOpenAI,
			payload:     `{"model":"gemini-4-hydrogen-xhigh","messages":[{"role":"user","content":"hi"}]}`,
			wantLevel:   "xhigh",
			wantInclude: true,
		},
		{
			name:        "claude-opus-5-5-medium claude request",
			model:       "claude-opus-5-5-medium",
			source:      sdktranslator.FormatClaude,
			payload:     `{"model":"claude-opus-5-5-medium","max_tokens":1024,"messages":[{"role":"user","content":"hi"}]}`,
			wantLevel:   "medium",
			wantInclude: true,
		},
		{
			name:        "gemini-4-argon-low gemini request",
			model:       "gemini-4-argon-low",
			source:      sdktranslator.FormatGemini,
			payload:     `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			wantLevel:   "low",
			wantInclude: true,
		},
		{
			name:        "explicit suffix overrides model tier",
			model:       "gemini-4-hydrogen-xhigh(low)",
			source:      sdktranslator.FormatOpenAI,
			payload:     `{"model":"gemini-4-hydrogen-xhigh(low)","messages":[{"role":"user","content":"hi"}]}`,
			wantLevel:   "low",
			wantInclude: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var errRead error
				capturedBody, errRead = io.ReadAll(r.Body)
				if errRead != nil {
					t.Fatalf("read upstream request: %v", errRead)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}}`)
			}))
			t.Cleanup(server.Close)

			executor := NewAntigravityExecutor(&config.Config{RequestRetry: 1})
			auth := &cliproxyauth.Auth{
				Metadata: map[string]any{
					"access_token": "token-123",
					"expired":      time.Now().Add(24 * time.Hour).Format(time.RFC3339),
					"project_id":   "project-1",
				},
				Attributes: map[string]string{"base_url": server.URL},
			}

			_, errExec := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
				Model:   tc.model,
				Payload: []byte(tc.payload),
			}, cliproxyexecutor.Options{
				SourceFormat:   tc.source,
				ResponseFormat: tc.source,
			})
			if errExec != nil {
				t.Fatalf("Execute() error = %v", errExec)
			}

			gotLevel := ojson.GetBytes(capturedBody, "request.generationConfig.thinkingConfig.thinkingLevel")
			if !gotLevel.Exists() || gotLevel.String() != tc.wantLevel {
				t.Fatalf("upstream thinkingLevel = %q (exists=%v), want %q; body=%s", gotLevel.String(), gotLevel.Exists(), tc.wantLevel, capturedBody)
			}
			gotInclude := ojson.GetBytes(capturedBody, "request.generationConfig.thinkingConfig.includeThoughts")
			if tc.wantInclude {
				if !gotInclude.Exists() || !gotInclude.Bool() {
					t.Fatalf("upstream includeThoughts = %v (exists=%v), want true; body=%s", gotInclude.Bool(), gotInclude.Exists(), capturedBody)
				}
			} else if gotInclude.Exists() {
				t.Fatalf("unexpected upstream includeThoughts = %v; body=%s", gotInclude.Bool(), capturedBody)
			}
			if ojson.GetBytes(capturedBody, "request.generationConfig.thinkingConfig.thinkingBudget").Exists() {
				t.Fatalf("unexpected thinkingBudget in upstream body: %s", capturedBody)
			}
		})
	}
}
