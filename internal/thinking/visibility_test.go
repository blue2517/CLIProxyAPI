package thinking

import "testing"

func TestClaudeThinkingIncludesContent(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		want       bool
		wantActive bool
	}{
		{name: "adaptive defaults visible", body: `{"thinking":{"type":"adaptive"}}`, want: true, wantActive: true},
		{name: "enabled defaults visible", body: `{"thinking":{"type":"enabled","budget_tokens":8192}}`, want: true, wantActive: true},
		{name: "auto compatibility defaults visible", body: `{"thinking":{"type":"auto"}}`, want: true, wantActive: true},
		{name: "summarized stays visible", body: `{"thinking":{"type":"adaptive","display":"summarized"}}`, want: true, wantActive: true},
		{name: "omitted stays hidden", body: `{"thinking":{"type":"adaptive","display":"omitted"}}`, wantActive: true},
		{name: "disabled stays absent", body: `{"thinking":{"type":"disabled"}}`},
		{name: "zero budget stays absent", body: `{"thinking":{"type":"enabled","budget_tokens":0}}`},
		{name: "missing thinking stays absent", body: `{}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, active := ClaudeThinkingIncludesContent([]byte(test.body))
			if got != test.want || active != test.wantActive {
				t.Fatalf("ClaudeThinkingIncludesContent() = (%v, %v), want (%v, %v)", got, active, test.want, test.wantActive)
			}
		})
	}
}
