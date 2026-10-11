package executor

import (
	"fmt"
	"io"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestAntigravityNativeFlashTierIDsAreIndependentOfEffort(t *testing.T) {
	for _, version := range []string{"3.8"} {
		for _, tc := range []struct {
			tier   string
			suffix string
			effort string
			want   string
		}{
			{tier: "high", effort: "medium", want: "medium"},
			{tier: "medium", effort: "high", want: "high"},
			{tier: "low", effort: "high", want: "high"},
			{tier: "high", suffix: "(medium)", effort: "high", want: "medium"},
			{tier: "medium", suffix: "(low)", effort: "high", want: "low"},
		} {
			modelID := "gemini-" + version + "-flash-" + tc.tier
			model := modelID + tc.suffix
			t.Run(model+"/"+tc.effort, func(t *testing.T) {
				source := []byte(fmt.Sprintf(`{"model":%q,"input":"hello","reasoning":{"effort":%q}}`, model, tc.effort))
				req := cliproxyexecutor.Request{Model: model, Payload: source}
				baseModel := thinking.ParseSuffix(model).ModelName
				body := sdktranslator.TranslateRequest(sdktranslator.FormatOpenAIResponse, sdktranslator.FormatAntigravity, baseModel, source, false)
				body, errApply := helps.ApplyRequestThinking(body, req, cliproxyexecutor.Options{}, "openai-response", "antigravity", "antigravity")
				if errApply != nil {
					t.Fatal(errApply)
				}
				executor := &AntigravityExecutor{}
				auth := &cliproxyauth.Auth{Metadata: map[string]any{"project_id": "project-1"}}
				for _, stream := range []bool{false, true} {
					request, errBuild := executor.buildRequest(t.Context(), auth, "test-token", baseModel, body, stream, "", "https://example.com")
					if errBuild != nil {
						t.Fatal(errBuild)
					}
					payload, errRead := io.ReadAll(request.Body)
					if errClose := request.Body.Close(); errClose != nil {
						t.Fatal(errClose)
					}
					if errRead != nil {
						t.Fatal(errRead)
					}
					if got := gjson.GetBytes(payload, "model").String(); got != modelID {
						t.Fatalf("stream=%v model ID = %q, want %q", stream, got, modelID)
					}
					if got := gjson.GetBytes(payload, "request.generationConfig.thinkingConfig.thinkingLevel").String(); got != tc.want {
						t.Fatalf("stream=%v thinking level = %q, want %q", stream, got, tc.want)
					}
				}
			})
		}
	}
}
