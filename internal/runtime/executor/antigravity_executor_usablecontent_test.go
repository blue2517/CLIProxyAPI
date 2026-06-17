package executor

import "testing"

// TestAntigravityResponseHasUsableContent verifies the empty-content detector that
// guards against gemini-3.1-pro returning finishReason=STOP with no usable parts.
// A response counts as usable when any candidate part carries non-empty text, a
// function call, or inline data; a part holding only a thoughtSignature (which
// Gemini 3.x emits even when no thinking was requested) does not count.
func TestAntigravityResponseHasUsableContent(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "empty text only (the compact failure mode)",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]},"finishReason":"STOP"}]}}`,
			want: false,
		},
		{
			name: "whitespace-only text",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"   \n\t"}]},"finishReason":"STOP"}]}}`,
			want: false,
		},
		{
			name: "thoughtSignature only, no usable content",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"abc123"}]},"finishReason":"STOP"}]}}`,
			want: false,
		},
		{
			name: "empty text plus thoughtSignature still not usable",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"","thoughtSignature":"abc123"}]}}]}}`,
			want: false,
		},
		{
			name: "non-empty text",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}]}}`,
			want: true,
		},
		{
			name: "function call with empty text is usable (tool call not误伤)",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":"","functionCall":{"name":"do_it","args":{}}}]},"finishReason":"STOP"}]}}`,
			want: true,
		},
		{
			name: "function call with no text field is usable",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"do_it","args":{"x":1}}}]}}]}}`,
			want: true,
		},
		{
			name: "mixed parts: empty text then function call",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""},{"functionCall":{"name":"do_it","args":{}}}]}}]}}`,
			want: true,
		},
		{
			name: "mixed parts: thoughtSignature then function call",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"sig"},{"functionCall":{"name":"do_it","args":{}}}]}}]}}`,
			want: true,
		},
		{
			name: "inlineData camelCase is usable",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"AAAA"}}]}}]}}`,
			want: true,
		},
		{
			name: "inline_data snake_case is usable",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"inline_data":{"mime_type":"image/png","data":"AAAA"}}]}}]}}`,
			want: true,
		},
		{
			name: "bare candidates wrapper (no response envelope)",
			raw:  `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]}}]}`,
			want: true,
		},
		{
			name: "bare candidates wrapper, empty text",
			raw:  `{"candidates":[{"content":{"role":"model","parts":[{"text":""}]}}]}`,
			want: false,
		},
		{
			name: "second candidate carries the content",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[{"text":""}]}},{"content":{"role":"model","parts":[{"text":"hi"}]}}]}}`,
			want: true,
		},
		{
			name: "candidate with no parts array",
			raw:  `{"response":{"candidates":[{"content":{"role":"model"},"finishReason":"STOP"}]}}`,
			want: false,
		},
		{
			name: "empty parts array",
			raw:  `{"response":{"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP"}]}}`,
			want: false,
		},
		{
			name: "no candidates field",
			raw:  `{"response":{}}`,
			want: false,
		},
		{
			name: "candidates not an array",
			raw:  `{"response":{"candidates":{}}}`,
			want: false,
		},
		{
			name: "empty object",
			raw:  `{}`,
			want: false,
		},
		{
			name: "invalid json",
			raw:  `not json at all`,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := antigravityResponseHasUsableContent([]byte(tt.raw)); got != tt.want {
				t.Errorf("antigravityResponseHasUsableContent() = %v, want %v\nraw: %s", got, tt.want, tt.raw)
			}
		})
	}
}
