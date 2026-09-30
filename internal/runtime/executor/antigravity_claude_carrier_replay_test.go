package executor

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	internalcache "github.com/router-for-me/CLIProxyAPI/v8/internal/cache"
	claudetranslator "github.com/router-for-me/CLIProxyAPI/v8/internal/translator/antigravity/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestAntigravityClaudeCarrierFreeMultiTurnSignatureReplay(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", streaming), func(t *testing.T) {
			internalcache.ClearAntigravityReasoningReplayCache()
			t.Cleanup(internalcache.ClearAntigravityReasoningReplayCache)
			const model = "gemini-3.6-flash-high"
			ctx := context.Background()
			scope := antigravityReasoningReplayScope{modelName: model, sessionKey: "session:carrier-free-test"}
			request := []byte(`{"model":"gemini-3.6-flash-high","tools":[{"name":"Read","input_schema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}],"messages":[{"role":"user","content":"hi"}]}`)
			payload := claudetranslator.ConvertClaudeRequestToAntigravity(model, request, false)
			wantTools := make(map[string]string)
			wantText := make(map[string]string)
			for round := 1; round <= 2; round++ {
				text := fmt.Sprintf("answer %d", round)
				textSignature := fmt.Sprintf("text-signature-%d-%s", round, strings.Repeat("x", 64))
				wantText[text] = textSignature
				response := []byte(`{"response":{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"totalTokenCount":12},"modelVersion":"gemini-3.6-flash","responseId":"carrier-free"}}`)
				parts := []byte(`[{"text":"` + text + `","thoughtSignature":"` + textSignature + `"}]`)
				for _, path := range []string{"/a", "/b"} {
					id := fmt.Sprintf("native-%d-%s", round, path[1:])
					signature := fmt.Sprintf("tool-signature-%s-%s", id, strings.Repeat("x", 64))
					wantTools[id] = signature
					part := []byte(`{"functionCall":{"id":"` + id + `","name":"Read","args":{"path":"` + path + `"}},"thoughtSignature":"` + signature + `"}`)
					var errSet error
					parts, errSet = sjson.SetRawBytes(parts, "-1", part)
					if errSet != nil {
						t.Fatal(errSet)
					}
				}
				var errSet error
				response, errSet = sjson.SetRawBytes(response, "response.candidates.0.content.parts", parts)
				if errSet != nil {
					t.Fatal(errSet)
				}
				if streaming {
					acc := newAntigravityReasoningReplayAccumulator(scope, payload)
					acc.ObserveSSELine(append([]byte("data: "), response...))
					acc.Commit(ctx)
					var param any
					stream := bytes.Join(claudetranslator.ConvertAntigravityResponseToClaude(ctx, model, request, request, response, &param), nil)
					if bytes.Contains(stream, []byte(`"content_block":{"type":"thinking"`)) {
						t.Fatalf("tool stream exposed an empty thinking carrier: %s", stream)
					}
				} else {
					cacheAntigravityReasoningReplayFromResponse(ctx, scope, payload, response)
				}
				claudeResponse := claudetranslator.ConvertAntigravityResponseToClaudeNonStream(ctx, model, request, request, response, nil)
				content := gjson.GetBytes(claudeResponse, "content")
				results := []byte(`[]`)
				for _, block := range content.Array() {
					if block.Get("type").String() == "thinking" {
						t.Fatalf("tool response exposed an empty thinking carrier: %s", claudeResponse)
					}
					if block.Get("type").String() == "tool_use" {
						result := []byte(`{"type":"tool_result","tool_use_id":"` + block.Get("id").String() + `","content":"ok"}`)
						results, errSet = sjson.SetRawBytes(results, "-1", result)
						if errSet != nil {
							t.Fatal(errSet)
						}
					}
				}
				assistant := []byte(`{"role":"assistant","content":` + content.Raw + `}`)
				request, errSet = sjson.SetRawBytes(request, "messages.-1", assistant)
				if errSet != nil {
					t.Fatal(errSet)
				}
				request, errSet = sjson.SetRawBytes(request, "messages.-1", []byte(`{"role":"user","content":`+string(results)+`}`))
				if errSet != nil {
					t.Fatal(errSet)
				}
				nextPayload := claudetranslator.ConvertClaudeRequestToAntigravity(model, request, false)
				items, found := internalcache.GetAntigravityReasoningReplayItems(model, scope.sessionKey)
				if !found {
					t.Fatal("raw response signatures were not retained")
				}
				var changed bool
				payload, changed = applyAntigravityReasoningReplayItems(nextPayload, items, nil)
				if !changed {
					t.Fatalf("raw signature replay did not update the tool history: %s", nextPayload)
				}
				seenTools := make(map[string]string)
				seenText := make(map[string]string)
				for _, nativeContent := range gjson.GetBytes(payload, "request.contents").Array() {
					for _, part := range nativeContent.Get("parts").Array() {
						if call := part.Get("functionCall"); call.Exists() {
							seenTools[call.Get("id").String()] = part.Get("thoughtSignature").String()
						}
						if text := part.Get("text").String(); text != "" {
							seenText[text] = part.Get("thoughtSignature").String()
						}
					}
				}
				for id, signature := range wantTools {
					if seenTools[id] != signature {
						t.Fatalf("round %d tool %s signature = %q, want %q; payload=%s", round, id, seenTools[id], signature, payload)
					}
				}
				for text, signature := range wantText {
					if seenText[text] != signature {
						t.Fatalf("round %d text %q signature = %q, want %q; payload=%s", round, text, seenText[text], signature, payload)
					}
				}
			}
		})
	}
}
