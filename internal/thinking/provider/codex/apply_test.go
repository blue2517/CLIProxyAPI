package codex

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/tidwall/gjson"
)

func TestApplyModeNone(t *testing.T) {
	tests := []struct {
		name                 string
		clampUnsupportedNone bool
		zeroAllowed          bool
		want                 string
	}{
		{
			name: "default forwards none",
			want: "none",
		},
		{
			name:                 "legacy clamp uses lowest level",
			clampUnsupportedNone: true,
			want:                 "low",
		},
		{
			name:                 "legacy clamp preserves supported none",
			clampUnsupportedNone: true,
			zeroAllowed:          true,
			want:                 "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelInfo := &registry.ModelInfo{
				ID: "codex-test",
				Thinking: &registry.ThinkingSupport{
					ZeroAllowed: tt.zeroAllowed,
					Levels:      []string{"low", "medium", "high"},
				},
			}
			config := thinking.ThinkingConfig{
				Mode:                 thinking.ModeNone,
				ClampUnsupportedNone: tt.clampUnsupportedNone,
			}

			got, err := NewApplier().Apply([]byte(`{"reasoning":{"effort":"medium","summary":"auto"}}`), config, modelInfo)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if effort := gjson.GetBytes(got, "reasoning.effort").String(); effort != tt.want {
				t.Fatalf("reasoning.effort = %q, want %q; body=%s", effort, tt.want, string(got))
			}
			if summary := gjson.GetBytes(got, "reasoning.summary"); tt.want == "none" && summary.Exists() {
				t.Fatalf("reasoning.summary should be removed when effort is none; body=%s", string(got))
			}
		})
	}
}
