package thinking_test

import (
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/thinking/provider/antigravity"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/thinking/provider/claude"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/thinking/provider/gemini"
)

func TestApplyThinkingAntigravityTierDefaultsWhenOmitted(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		fromFormat string
		sourceBody string
		wantLevel  string
	}{
		{name: "hydrogen xhigh from openai", model: "gemini-4-hydrogen-xhigh", fromFormat: "openai", sourceBody: `{"model":"gemini-4-hydrogen-xhigh","messages":[{"role":"user","content":"hi"}]}`, wantLevel: "xhigh"},
		{name: "hydrogen high from openai", model: "gemini-4-hydrogen-high", fromFormat: "openai", sourceBody: `{"model":"gemini-4-hydrogen-high","messages":[{"role":"user","content":"hi"}]}`, wantLevel: "high"},
		{name: "argon low from gemini", model: "gemini-4-argon-low", fromFormat: "gemini", sourceBody: `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, wantLevel: "low"},
		{name: "carbon medium from claude", model: "gemini-4-carbon-medium", fromFormat: "claude", sourceBody: `{"model":"gemini-4-carbon-medium","messages":[{"role":"user","content":"hi"}]}`, wantLevel: "medium"},
		{name: "claude opus 5.5 medium from claude", model: "claude-opus-5-5-medium", fromFormat: "claude", sourceBody: `{"model":"claude-opus-5-5-medium","messages":[{"role":"user","content":"hi"}]}`, wantLevel: "medium"},
		{name: "claude sonnet 5.5 high from openai", model: "claude-sonnet-5-5-high", fromFormat: "openai", sourceBody: `{"model":"claude-sonnet-5-5-high","messages":[{"role":"user","content":"hi"}]}`, wantLevel: "high"},
		{name: "gemini 3.8 flash low from antigravity", model: "gemini-3.8-flash-low", fromFormat: "antigravity", sourceBody: `{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`, wantLevel: "low"},
	}

	const baseAntigravityBody = `{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := thinking.ApplyThinking([]byte(baseAntigravityBody), tc.model, tc.fromFormat, "antigravity", "antigravity")
			if err != nil {
				t.Fatalf("ApplyThinking() error = %v", err)
			}
			gotLevel := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel")
			if !gotLevel.Exists() || gotLevel.String() != tc.wantLevel {
				t.Fatalf("thinkingLevel = %q (exists=%v), want %q; body=%s", gotLevel.String(), gotLevel.Exists(), tc.wantLevel, out)
			}
			gotInclude := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.includeThoughts")
			if !gotInclude.Exists() || !gotInclude.Bool() {
				t.Fatalf("includeThoughts = %v (exists=%v), want true; body=%s", gotInclude.Bool(), gotInclude.Exists(), out)
			}
			if ojson.GetBytes(out, "request.generationConfig.thinkingConfig.thinkingBudget").Exists() {
				t.Fatalf("unexpected thinkingBudget when level defaulted; body=%s", out)
			}

			modelInfo := registry.LookupStaticModelInfoByChannel(tc.model, "antigravity")
			if modelInfo == nil {
				t.Fatalf("missing static catalog entry for %s", tc.model)
			}
			outWithInfo, errWithInfo := thinking.ApplyThinkingWithModelInfo([]byte(baseAntigravityBody), []byte(tc.sourceBody), tc.model, tc.fromFormat, "antigravity", "antigravity", modelInfo)
			if errWithInfo != nil {
				t.Fatalf("ApplyThinkingWithModelInfo() error = %v", errWithInfo)
			}
			if got := ojson.GetBytes(outWithInfo, "request.generationConfig.thinkingConfig.thinkingLevel").String(); got != tc.wantLevel {
				t.Fatalf("withModelInfo thinkingLevel = %q, want %q; body=%s", got, tc.wantLevel, outWithInfo)
			}
			if got := ojson.GetBytes(outWithInfo, "request.generationConfig.thinkingConfig.includeThoughts"); !got.Exists() || !got.Bool() {
				t.Fatalf("withModelInfo includeThoughts = %v (exists=%v), want true; body=%s", got.Bool(), got.Exists(), outWithInfo)
			}
		})
	}
}

func TestApplyThinkingAntigravityTierDefaultsRespectExplicitOverrides(t *testing.T) {
	const baseBody = `{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`

	t.Run("suffix overrides model tier", func(t *testing.T) {
		out, err := thinking.ApplyThinking([]byte(baseBody), "gemini-4-hydrogen-xhigh(low)", "openai", "antigravity", "antigravity")
		if err != nil {
			t.Fatalf("ApplyThinking() error = %v", err)
		}
		if got := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel").String(); got != "low" {
			t.Fatalf("thinkingLevel = %q, want low; body=%s", got, out)
		}
	})

	t.Run("explicit reasoning effort overrides model tier", func(t *testing.T) {
		modelInfo := registry.LookupStaticModelInfoByChannel("gemini-4-hydrogen-xhigh", "antigravity")
		source := []byte(`{"model":"gemini-4-hydrogen-xhigh","reasoning_effort":"medium","messages":[{"role":"user","content":"hi"}]}`)
		body := []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"thinkingLevel":"medium"}}}}`)
		out, err := thinking.ApplyThinkingWithModelInfo(body, source, "gemini-4-hydrogen-xhigh", "openai", "antigravity", "antigravity", modelInfo)
		if err != nil {
			t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
		}
		if got := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel").String(); got != "medium" {
			t.Fatalf("thinkingLevel = %q, want medium; body=%s", got, out)
		}
	})

	t.Run("explicit includeThoughts false preserved while level defaulted", func(t *testing.T) {
		modelInfo := registry.LookupStaticModelInfoByChannel("gemini-4-argon-high", "antigravity")
		source := []byte(`{"generationConfig":{"thinkingConfig":{"includeThoughts":false}}}`)
		body := []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"thinkingConfig":{"includeThoughts":false}}}}`)
		out, err := thinking.ApplyThinkingWithModelInfo(body, source, "gemini-4-argon-high", "gemini", "antigravity", "antigravity", modelInfo)
		if err != nil {
			t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
		}
		if got := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel").String(); got != "high" {
			t.Fatalf("thinkingLevel = %q, want high; body=%s", got, out)
		}
		gotInclude := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.includeThoughts")
		if !gotInclude.Exists() || gotInclude.Bool() {
			t.Fatalf("includeThoughts = %v (exists=%v), want false; body=%s", gotInclude.Bool(), gotInclude.Exists(), out)
		}
	})

	t.Run("explicit disable suffix none is not overwritten", func(t *testing.T) {
		out, err := thinking.ApplyThinking([]byte(baseBody), "claude-opus-5-5-high(none)", "claude", "antigravity", "antigravity")
		if err != nil {
			t.Fatalf("ApplyThinking() error = %v", err)
		}
		if got := ojson.GetBytes(out, "request.generationConfig.thinkingConfig"); got.Exists() {
			t.Fatalf("expected thinkingConfig to remain absent on (none), got %s", out)
		}
	})

	t.Run("alias falls back to resolved modelInfo ID", func(t *testing.T) {
		modelInfo := registry.LookupStaticModelInfoByChannel("gemini-4-hydrogen-medium", "antigravity")
		out, err := thinking.ApplyThinkingWithModelInfo([]byte(baseBody), nil, "my-custom-alias", "openai", "antigravity", "antigravity", modelInfo)
		if err != nil {
			t.Fatalf("ApplyThinkingWithModelInfo() error = %v", err)
		}
		if got := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.thinkingLevel").String(); got != "medium" {
			t.Fatalf("thinkingLevel = %q, want medium; body=%s", got, out)
		}
		if got := ojson.GetBytes(out, "request.generationConfig.thinkingConfig.includeThoughts"); !got.Exists() || !got.Bool() {
			t.Fatalf("includeThoughts = %v (exists=%v), want true; body=%s", got.Bool(), got.Exists(), out)
		}
	})
}

func TestApplyThinkingAntigravityTierDefaultsIgnoreNonTieredAndOtherProviders(t *testing.T) {
	const baseBody = `{"request":{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}}`

	for _, model := range []string{"gemini-3-flash", "claude-opus-4-6-thinking", "gpt-oss-120b-medium"} {
		out, err := thinking.ApplyThinking([]byte(baseBody), model, "openai", "antigravity", "antigravity")
		if err != nil {
			t.Fatalf("ApplyThinking(%s) error = %v", model, err)
		}
		if ojson.GetBytes(out, "request.generationConfig.thinkingConfig").Exists() {
			t.Fatalf("unexpected thinkingConfig injected for %s: %s", model, out)
		}
	}

	syntheticGemini := &registry.ModelInfo{
		ID:       "gemini-4-hydrogen-high",
		Type:     "gemini",
		Thinking: &registry.ThinkingSupport{Min: 1, Max: 65535, Levels: []string{"low", "medium", "high", "xhigh"}},
	}
	geminiBody := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	out, err := thinking.ApplyThinkingWithModelInfo(geminiBody, nil, "gemini-4-hydrogen-high", "openai", "gemini", "gemini", syntheticGemini)
	if err != nil {
		t.Fatalf("ApplyThinkingWithModelInfo(gemini) error = %v", err)
	}
	if ojson.GetBytes(out, "generationConfig.thinkingConfig").Exists() {
		t.Fatalf("unexpected thinkingConfig injected for non-antigravity provider: %s", out)
	}
}
