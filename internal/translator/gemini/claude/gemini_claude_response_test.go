package claude

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestConvertGeminiResponseToClaude_SignatureOnlyPartDoesNotOpenEmptyTextBlock(t *testing.T) {
	requestJSON := []byte(`{"model":"gemini-test","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	thinkingChunk := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "thinking text", "thought": true}]
			}
		}],
		"modelVersion": "gemini-test",
		"responseId": "resp-test"
	}`)
	signatureChunk := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "", "thoughtSignature": "sig-test"}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 10,
			"thoughtsTokenCount": 2,
			"totalTokenCount": 12
		},
		"modelVersion": "gemini-test",
		"responseId": "resp-test"
	}`)

	var param any
	ctx := context.Background()
	output := bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-test", requestJSON, requestJSON, thinkingChunk, &param), nil)
	output = append(output, bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-test", requestJSON, requestJSON, signatureChunk, &param), nil)...)
	output = append(output, bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-test", requestJSON, requestJSON, []byte("[DONE]"), &param), nil)...)
	outputText := string(output)

	if strings.Contains(outputText, `"content_block":{"type":"text"`) {
		t.Fatalf("signature-only part must not open an empty text block: %s", outputText)
	}
	if strings.Contains(outputText, `"type":"content_block_stop","index":1`) {
		t.Fatalf("signature-only part must not produce a stop for unopened index 1: %s", outputText)
	}
	if !strings.Contains(outputText, `"type":"signature_delta"`) || !strings.Contains(outputText, `"signature":"sig-test"`) {
		t.Fatalf("signature-only part must be emitted as a thinking signature delta: %s", outputText)
	}
	if got := strings.Count(outputText, `"type":"content_block_stop","index":0`); got != 1 {
		t.Fatalf("expected exactly one stop for thinking index 0, got %d: %s", got, outputText)
	}
	if !strings.Contains(outputText, `"type":"message_delta"`) || !strings.Contains(outputText, `"output_tokens":2`) {
		t.Fatalf("finish chunk without candidatesTokenCount must still emit final message_delta: %s", outputText)
	}
	if !strings.Contains(outputText, `"type":"message_stop"`) {
		t.Fatalf("DONE chunk must still emit message_stop after final events: %s", outputText)
	}
}

func TestConvertGeminiResponseToClaude_ThinkingDisabled_SignatureDiscarded(t *testing.T) {
	requestJSON := []byte(`{"model":"gemini-3-flash","messages":[{"role":"user","content":[{"type":"text","text":"test"}]}]}`)

	// Text-only chunk (no thought flag)
	textChunk := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"text": "<block>no</block>"}]
			}
		}],
		"modelVersion": "gemini-3-flash",
		"responseId": "resp-cls"
	}`)

	// Trailing signature from Gemini 3.x (no thought flag)
	sigChunk := []byte(`{
		"candidates": [{
			"content": {
				"parts": [{"thoughtSignature": "EjQKSomeSignature", "text": ""}]
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 100,
			"candidatesTokenCount": 5,
			"totalTokenCount": 105
		},
		"modelVersion": "gemini-3-flash",
		"responseId": "resp-cls"
	}`)

	var param any
	ctx := context.Background()
	output := bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-3-flash", requestJSON, requestJSON, textChunk, &param), nil)
	output = append(output, bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-3-flash", requestJSON, requestJSON, sigChunk, &param), nil)...)
	output = append(output, bytes.Join(ConvertGeminiResponseToClaude(ctx, "gemini-3-flash", requestJSON, requestJSON, []byte("[DONE]"), &param), nil)...)
	outputText := string(output)

	if strings.Contains(outputText, `"type":"thinking"`) {
		t.Fatalf("trailing thoughtSignature without thought:true must NOT create thinking block: %s", outputText)
	}
	if strings.Contains(outputText, `"type":"thinking_delta"`) {
		t.Fatalf("trailing thoughtSignature without thought:true must NOT create thinking deltas: %s", outputText)
	}
	if strings.Contains(outputText, `"type":"signature_delta"`) {
		t.Fatalf("trailing thoughtSignature without thought:true must NOT emit signature delta: %s", outputText)
	}
	if !strings.Contains(outputText, `"type":"text"`) {
		t.Fatalf("text content must remain as text block: %s", outputText)
	}
	if !strings.Contains(outputText, `<block>no</block>`) {
		t.Fatalf("classifier text must be preserved: %s", outputText)
	}
}
