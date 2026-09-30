package claude

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestLeadingDetachedSignatureBindsRealThoughtWithEitherThoughtFlag(t *testing.T) {
	for _, thought := range []bool{false, true} {
		t.Run(fmt.Sprintf("thought=%t", thought), func(t *testing.T) {
			signature := testGeminiEPrefixSignature(t)
			wireSignature := formatGeminiClaudeCarrierValue("gemini-3.6-flash-high", signature, geminiClaudeCarrierStandalone, geminiClaudeCarrierText)
			request := []byte(`{"model":"gemini-3.6-flash-high"}`)
			response := []byte(fmt.Sprintf(`{"response":{"candidates":[{"content":{"parts":[{"text":"","thought":%t,"thoughtSignature":%q},{"text":"reason","thought":true}]},"finishReason":"STOP"}],"modelVersion":"gemini-3.6-flash","responseId":"leading-thought"}}`, thought, signature))
			output := ConvertAntigravityResponseToClaudeNonStream(context.Background(), "gemini-3.6-flash-high", request, request, response, nil)
			content := gjson.GetBytes(output, "content").Array()
			if len(content) != 1 || content[0].Get("thinking").String() != "reason" || content[0].Get("signature").String() != wireSignature {
				t.Fatalf("leading signature must bind the single real thinking block: %s", output)
			}
			var param any
			stream := string(bytes.Join(ConvertAntigravityResponseToClaude(context.Background(), "gemini-3.6-flash-high", request, request, response, &param), nil))
			if strings.Count(stream, `"content_block":{"type":"thinking"`) != 1 || !strings.Contains(stream, `"type":"signature_delta","signature":"`+wireSignature+`"`) {
				t.Fatalf("leading signature must bind the single real streamed thinking block: %s", stream)
			}
		})
	}
}

func TestSignatureOnlyResponseNeverCreatesThinkingBlock(t *testing.T) {
	for _, thought := range []bool{false, true} {
		t.Run(fmt.Sprintf("thought=%t", thought), func(t *testing.T) {
			request := []byte(`{"model":"gemini-3.6-flash-high"}`)
			response := []byte(fmt.Sprintf(`{"response":{"candidates":[{"content":{"parts":[{"text":"","thought":%t,"thoughtSignature":%q}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":0,"totalTokenCount":1},"modelVersion":"gemini-3.6-flash","responseId":"signature-only"}}`, thought, testGeminiEPrefixSignature(t)))
			output := ConvertAntigravityResponseToClaudeNonStream(context.Background(), "gemini-3.6-flash-high", request, request, response, nil)
			for _, block := range gjson.GetBytes(output, "content").Array() {
				if block.Get("type").String() == "thinking" {
					t.Fatalf("signature-only response exposed a thinking carrier: %s", output)
				}
			}
			var param any
			stream := bytes.Join(ConvertAntigravityResponseToClaude(context.Background(), "gemini-3.6-flash-high", request, request, response, &param), nil)
			if bytes.Contains(stream, []byte(`"content_block":{"type":"thinking"`)) {
				t.Fatalf("signature-only stream exposed a thinking carrier: %s", stream)
			}
		})
	}
}
