package executor

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	internalcache "github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	_ "github.com/router-for-me/CLIProxyAPI/v7/internal/translator"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func validCodexReasoningEncryptedContentForTestSeed(seed byte) string {
	payload := make([]byte, 1+8+16+16+32)
	payload[0] = 0x80
	for i := 9; i < len(payload); i++ {
		payload[i] = seed + byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func shortenedCodexReplayCallIDForTest(id string) string {
	const limit = 64
	if len(id) <= limit {
		return id
	}

	sum := sha256.Sum256([]byte(id))
	suffix := "_" + hex.EncodeToString(sum[:8])
	prefixLen := limit - len(suffix)
	if prefixLen <= 0 {
		return suffix[len(suffix)-limit:]
	}
	return id[:prefixLen] + suffix
}

func TestCodexExecutorReasoningReplayCacheStoresFinalDoneAndInjectsNextClaudeRequest(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	addedEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(1)
	doneEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(2)
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		bodies = append(bodies, body)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.added","item":{"id":"rs_added","type":"reasoning","status":"in_progress","summary":[],"encrypted_content":"` + addedEncryptedContent + `"},"output_index":0}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"rs_done","type":"reasoning","summary":[],"encrypted_content":"` + doneEncryptedContent + `"},"output_index":0}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "auth-replay-1",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-1\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`),
	}, opts)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}

	_, err = executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-1\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}, opts)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("upstream request count = %d, want 2", len(bodies))
	}
	secondBody := bodies[1]
	if got := gjson.GetBytes(secondBody, "input.0.type").String(); got != "reasoning" {
		t.Fatalf("input.0.type = %q, want reasoning; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.0.encrypted_content").String(); got != doneEncryptedContent {
		t.Fatalf("injected encrypted_content = %q, want final done %q; body=%s", got, doneEncryptedContent, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.1.role").String(); got != "user" {
		t.Fatalf("input.1.role = %q, want user; body=%s", got, string(secondBody))
	}
}

func TestCodexExecutorReasoningReplayCacheSharesSameSessionAcrossClientKeys(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	from := sdktranslator.FromString("claude")
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-only\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}
	opts := cliproxyexecutor.Options{SourceFormat: from}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)
	encryptedContent := validCodexReasoningEncryptedContentForTestSeed(11)

	firstScope := codexReasoningReplayScopeFromRequest(codexReplaySessionOnlyContext("client-key-a"), from, req, opts, body)
	if !firstScope.valid() {
		t.Fatalf("first replay scope is invalid: %#v", firstScope)
	}
	cacheCodexReasoningReplayFromCompleted(firstScope, []byte(`{"response":{"output":[{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+encryptedContent+`"}]}}`))

	secondBody, secondScope := applyCodexReasoningReplayCache(codexReplaySessionOnlyContext("client-key-b"), from, req, opts, body)
	if secondScope != firstScope {
		t.Fatalf("replay scope should ignore client API key for the same session: first=%#v second=%#v", firstScope, secondScope)
	}
	if got := gjson.GetBytes(secondBody, "input.0.type").String(); got != "reasoning" {
		t.Fatalf("input.0.type = %q, want same-session replay; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.0.encrypted_content").String(); got != encryptedContent {
		t.Fatalf("injected encrypted_content = %q, want cached value", got)
	}
}

func TestCodexExecutorReasoningReplaySessionKeyUsesClaudeCodeJSONSessionID(t *testing.T) {
	from := sdktranslator.FromString("claude")
	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"\",\"session_id\":\"session-json-1\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)

	got := codexReasoningReplaySessionKey(context.Background(), from, req, cliproxyexecutor.Options{SourceFormat: from}, body)
	if got != "claude:session-json-1" {
		t.Fatalf("codexReasoningReplaySessionKey() = %q, want claude:session-json-1", got)
	}
}

func TestCodexExecutorReasoningReplaySessionKeySeparatesClaudeCodeAgents(t *testing.T) {
	from := sdktranslator.FromString("claude")
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"session_id\":\"shared-session\"}"}}`),
	}
	body := []byte(`{"model":"gpt-5.4","input":[]}`)
	firstOpts := cliproxyexecutor.Options{
		SourceFormat: from,
		Headers:      http.Header{helps.ClaudeCodeAgentHeader: []string{"agent-a"}},
	}
	secondOpts := cliproxyexecutor.Options{
		SourceFormat: from,
		Headers:      http.Header{helps.ClaudeCodeAgentHeader: []string{"agent-b"}},
	}

	first := codexReasoningReplaySessionKey(context.Background(), from, req, firstOpts, body)
	second := codexReasoningReplaySessionKey(context.Background(), from, req, secondOpts, body)
	if first != "claude:shared-session:agent:agent-a" {
		t.Fatalf("first replay session key = %q", first)
	}
	if second != "claude:shared-session:agent:agent-b" {
		t.Fatalf("second replay session key = %q", second)
	}
	if first == second {
		t.Fatalf("agent-specific replay session keys must differ")
	}
}

func TestCodexExecutorReasoningReplayCacheDoesNotLeakAcrossClaudeCodeAgents(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	from := sdktranslator.FromString("claude")
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"session_id\":\"shared-session\"}"}}`),
	}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)
	firstOpts := cliproxyexecutor.Options{
		SourceFormat: from,
		Headers:      http.Header{helps.ClaudeCodeAgentHeader: []string{"agent-a"}},
	}
	secondOpts := cliproxyexecutor.Options{
		SourceFormat: from,
		Headers:      http.Header{helps.ClaudeCodeAgentHeader: []string{"agent-b"}},
	}
	firstScope := codexReasoningReplayScopeFromRequest(context.Background(), from, req, firstOpts, body)
	if !firstScope.valid() {
		t.Fatalf("first replay scope is invalid: %#v", firstScope)
	}
	cacheCodexReasoningReplayFromCompleted(firstScope, []byte(`{"response":{"output":[{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+validCodexReasoningEncryptedContentForTestSeed(13)+`"}]}}`))

	updated, secondScope := applyCodexReasoningReplayCache(context.Background(), from, req, secondOpts, body)
	if secondScope == firstScope {
		t.Fatalf("different agents must use different replay scopes: %#v", secondScope)
	}
	if got := gjson.GetBytes(updated, "input.0.type").String(); got != "message" {
		t.Fatalf("agent B received agent A replay item: input.0.type = %q; body=%s", got, string(updated))
	}
}

func TestCodexExecutorReasoningReplayCacheSkipsExplicitlyDisabledClaudeThinking(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	from := sdktranslator.FromString("claude")
	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"session_id\":\"disabled-thinking-session\"}"},
			"thinking":{"type":"disabled"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"developer","content":[{"type":"input_text","text":"stable instructions"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)
	internalcache.CacheCodexReasoningReplayItem("gpt-5.4", "claude:disabled-thinking-session", []byte(`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+validCodexReasoningEncryptedContentForTestSeed(14)+`"}`))

	updated, scope := applyCodexReasoningReplayCache(context.Background(), from, req, cliproxyexecutor.Options{SourceFormat: from}, body)
	if scope.valid() {
		t.Fatalf("disabled Claude thinking unexpectedly created replay scope: %#v", scope)
	}
	if string(updated) != string(body) {
		t.Fatalf("disabled Claude thinking injected replay data: %s", string(updated))
	}
}

func TestCodexExecutorReasoningReplaySessionKeyRejectsBareClaudeUserID(t *testing.T) {
	from := sdktranslator.FromString("claude")
	req := cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"same-user-across-chats"},"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}
	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)

	got := codexReasoningReplaySessionKey(context.Background(), from, req, cliproxyexecutor.Options{SourceFormat: from}, body)
	if got != "" {
		t.Fatalf("bare metadata.user_id must not become replay session key, got %q", got)
	}
}

func TestCodexExecutorReasoningReplaySessionKeyCanonicalizesSessionHeaderAliases(t *testing.T) {
	legacy := http.Header{"Session_id": []string{"session-alias"}}
	lowercase := http.Header{"session_id": []string{"session-alias"}}
	canonical := http.Header{"Session-Id": []string{"session-alias"}}

	gotLegacy := codexReasoningReplaySessionKeyFromHeaders(legacy)
	gotLowercase := codexReasoningReplaySessionKeyFromHeaders(lowercase)
	gotCanonical := codexReasoningReplaySessionKeyFromHeaders(canonical)

	if gotLegacy != gotLowercase || gotLowercase != gotCanonical {
		t.Fatalf("session header aliases produced different keys: legacy=%q lowercase=%q canonical=%q", gotLegacy, gotLowercase, gotCanonical)
	}
	if gotCanonical != "session-id:session-alias" {
		t.Fatalf("canonical session key = %q, want session-id:session-alias", gotCanonical)
	}
}

func TestCodexExecutorReasoningReplaySessionKeyCanonicalizesWindowHeaderWithPayload(t *testing.T) {
	payload := []byte(`{"client_metadata":{"x-codex-window-id":"window-1"}}`)
	headers := http.Header{"X-Codex-Window-Id": []string{"window-1"}}

	gotPayload := codexReasoningReplaySessionKeyFromPayload(payload)
	gotHeader := codexReasoningReplaySessionKeyFromHeaders(headers)

	if gotPayload != gotHeader {
		t.Fatalf("window replay keys differ: payload=%q header=%q", gotPayload, gotHeader)
	}
	if gotHeader != "window:window-1" {
		t.Fatalf("window replay key = %q, want window:window-1", gotHeader)
	}
}

func TestCodexExecutorReasoningReplayCacheSharesSameSessionAcrossCodexAuths(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	encryptedContent := validCodexReasoningEncryptedContentForTestSeed(12)
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		bodies = append(bodies, body)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"rs_done","type":"reasoning","summary":[],"encrypted_content":"` + encryptedContent + `"},"output_index":0}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	firstAuth := &cliproxyauth.Auth{
		ID: "auth-replay-session-auth-a",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test-a",
		},
	}
	secondAuth := &cliproxyauth.Auth{
		ID: "auth-replay-session-auth-b",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test-b",
		},
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	_, err := executor.Execute(context.Background(), firstAuth, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-auth-switch\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`),
	}, opts)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}

	_, err = executor.Execute(context.Background(), secondAuth, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-auth-switch\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}, opts)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("upstream request count = %d, want 2", len(bodies))
	}
	secondBody := bodies[1]
	if got := gjson.GetBytes(secondBody, "input.0.type").String(); got != "reasoning" {
		t.Fatalf("input.0.type = %q, want same-session replay across auths; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.0.encrypted_content").String(); got != encryptedContent {
		t.Fatalf("injected encrypted_content = %q, want cached value", got)
	}
}

func codexReplaySessionOnlyContext(apiKey string) context.Context {
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Set("userApiKey", apiKey)
	ginCtx.Set("accessProvider", "config-inline")
	ginCtx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	return context.WithValue(context.Background(), "gin", ginCtx)
}

func TestCodexExecutorReasoningReplayCacheDoesNotInjectNativeResponsesRequest(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	cachedEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(3)
	internalcache.CacheCodexReasoningReplayItem("gpt-5.4", "prompt-cache:native-session", []byte(`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+cachedEncryptedContent+`"}`))

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.Execute(context.Background(), &cliproxyauth.Auth{
		ID: "auth-replay-native",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","prompt_cache_key":"native-session","input":[{"role":"user","content":"native"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(gotBody, "input.0.type").String(); got == "reasoning" {
		t.Fatalf("native Responses request should not receive cached reasoning; body=%s", string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.0.role").String(); got != "user" {
		t.Fatalf("input.0.role = %q, want user; body=%s", got, string(gotBody))
	}
}

func TestCodexExecutorReasoningReplayCacheDoesNotStoreNativeResponsesRequest(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	nativeEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[{"id":"rs_native","type":"reasoning","summary":[],"encrypted_content":"` + nativeEncryptedContent + `"}]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.Execute(context.Background(), &cliproxyauth.Auth{
		ID: "auth-replay-native-store",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","prompt_cache_key":"native-store","input":[{"role":"user","content":"native"}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if _, ok := internalcache.GetCodexReasoningReplayItem("gpt-5.4", "prompt-cache:native-store"); ok {
		t.Fatal("native Responses request should not populate Codex reasoning replay cache")
	}
}

func TestCodexExecutorReasoningReplayCacheDoesNotDuplicateClaudeClientReasoning(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	cachedEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(5)
	clientEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(6)
	internalcache.CacheCodexReasoningReplayItem("gpt-5.4", "claude:session-2", []byte(`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+cachedEncryptedContent+`"}`))

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.Execute(context.Background(), &cliproxyauth.Auth{
		ID: "auth-replay-2",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-2\"}"},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"client summary","signature":"` + clientEncryptedContent + `"},{"type":"text","text":"answer"}]},{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(gotBody, "input.0.encrypted_content").String(); got != clientEncryptedContent {
		t.Fatalf("client reasoning should be preserved, got %q want %q; body=%s", got, clientEncryptedContent, string(gotBody))
	}
	reasoningCount := 0
	for _, item := range gjson.GetBytes(gotBody, "input").Array() {
		if item.Get("type").String() == "reasoning" {
			reasoningCount++
		}
	}
	if reasoningCount != 1 {
		t.Fatalf("reasoning item count = %d, want 1; body=%s", reasoningCount, string(gotBody))
	}
}

func TestCodexExecutorReasoningReplayCacheInsertsReasoningBeforeAssistantOutputInClaudeHistory(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	cachedEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(7)
	internalcache.CacheCodexReasoningReplayItem("gpt-5.4", "claude:session-history", []byte(`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+cachedEncryptedContent+`"}`))

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.Execute(context.Background(), &cliproxyauth.Auth{
		ID: "auth-replay-history",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}, cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-history\"}"},
			"messages":[
				{"role":"user","content":[{"type":"text","text":"first"}]},
				{"role":"assistant","content":[{"type":"text","text":"answer"}]},
				{"role":"user","content":[{"type":"text","text":"next"}]}
			]
		}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if got := gjson.GetBytes(gotBody, "input.0.role").String(); got != "user" {
		t.Fatalf("input.0.role = %q, want first user message; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.1.type").String(); got != "reasoning" {
		t.Fatalf("input.1.type = %q, want cached reasoning before assistant output; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.1.encrypted_content").String(); got != cachedEncryptedContent {
		t.Fatalf("input.1.encrypted_content = %q, want cached reasoning; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.2.role").String(); got != "assistant" {
		t.Fatalf("input.2.role = %q, want assistant output after cached reasoning; body=%s", got, string(gotBody))
	}
	if got := gjson.GetBytes(gotBody, "input.3.role").String(); got != "user" {
		t.Fatalf("input.3.role = %q, want final user message; body=%s", got, string(gotBody))
	}
}

func TestCodexExecutorReasoningReplayCacheExecuteStreamStoresFinalDoneForClaude(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	addedEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(7)
	doneEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(8)
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		bodies = append(bodies, body)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.added","item":{"id":"rs_added","type":"reasoning","status":"in_progress","summary":[],"encrypted_content":"` + addedEncryptedContent + `"},"output_index":0}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"rs_done","type":"reasoning","summary":[],"encrypted_content":"` + doneEncryptedContent + `"},"output_index":0}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "auth-replay-stream",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}

	streamResult, err := executor.ExecuteStream(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"stream-session-1\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream error: %v", err)
	}
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error: %v", chunk.Err)
		}
	}

	_, err = executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"stream-session-1\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("upstream request count = %d, want 2", len(bodies))
	}
	secondBody := bodies[1]
	if got := gjson.GetBytes(secondBody, "input.0.encrypted_content").String(); got != doneEncryptedContent {
		t.Fatalf("stream cached encrypted_content = %q, want final done %q; body=%s", got, doneEncryptedContent, string(secondBody))
	}
}

func TestCodexExecutorReasoningReplayCacheClearsOnNonStreamResponseFailedInvalidSignature(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	cachedEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(9)
	internalcache.CacheCodexReasoningReplayItem("gpt-5.4", "claude:session-invalid-nonstream", []byte(`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+cachedEncryptedContent+`"}`))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"message":"Invalid signature in thinking block","type":"invalid_request_error","code":"invalid_request_error"}}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	_, err := executor.Execute(context.Background(), &cliproxyauth.Auth{
		ID: "auth-replay-invalid-nonstream",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-invalid-nonstream\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	})
	if err == nil {
		t.Fatal("expected invalid signature error")
	}
	if _, ok := internalcache.GetCodexReasoningReplayItem("gpt-5.4", "claude:session-invalid-nonstream"); ok {
		t.Fatal("invalid signature response.failed should clear cached replay item")
	}
}

func TestCodexExecutorReasoningReplayCacheClearsOnStreamResponseFailedInvalidSignature(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	cachedEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(10)
	internalcache.CacheCodexReasoningReplayItem("gpt-5.4", "claude:session-invalid-stream", []byte(`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+cachedEncryptedContent+`"}`))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"message":"Invalid signature in thinking block","type":"invalid_request_error","code":"invalid_request_error"}}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	streamResult, err := executor.ExecuteStream(context.Background(), &cliproxyauth.Auth{
		ID: "auth-replay-invalid-stream",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}, cliproxyexecutor.Request{
		Model:   "gpt-5.4",
		Payload: []byte(`{"model":"gpt-5.4","metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-invalid-stream\"}"},"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       true,
	})
	if err != nil {
		t.Fatalf("ExecuteStream setup error: %v", err)
	}

	gotChunkErr := false
	for chunk := range streamResult.Chunks {
		if chunk.Err != nil {
			gotChunkErr = true
		}
	}
	if !gotChunkErr {
		t.Fatal("expected stream chunk error for invalid signature response.failed")
	}
	if _, ok := internalcache.GetCodexReasoningReplayItem("gpt-5.4", "claude:session-invalid-stream"); ok {
		t.Fatal("invalid signature response.failed should clear cached replay item")
	}
}

func TestCodexExecutorReasoningReplayCacheReplaysFunctionCallForClaudeToolResult(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	reasoningEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(8)
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		bodies = append(bodies, body)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"` + reasoningEncryptedContent + `"},"output_index":0}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"weather\"}","status":"in_progress"},"output_index":1}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"q\":\"weather\"}","status":"completed"},"output_index":1}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "auth-replay-claude-tool",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"claude-session-tool\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"call lookup"}]}],
			"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]
		}`),
	}, opts)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}

	_, err = executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"claude-session-tool\"}"},
			"messages":[
				{"role":"user","content":[{"type":"text","text":"call lookup"}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"sunny"}]}
			],
			"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]
		}`),
	}, opts)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("upstream request count = %d, want 2", len(bodies))
	}
	secondBody := bodies[1]
	if got := gjson.GetBytes(secondBody, "input.0.type").String(); got != "message" {
		t.Fatalf("input.0.type = %q, want initial user message; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.1.type").String(); got != "reasoning" {
		t.Fatalf("input.1.type = %q, want cached reasoning; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.2.type").String(); got != "function_call" {
		t.Fatalf("input.2.type = %q, want cached function_call; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.2.call_id").String(); got != "call_1" {
		t.Fatalf("input.2.call_id = %q, want call_1; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.3.type").String(); got != "function_call_output" {
		t.Fatalf("input.3.type = %q, want function_call_output after cached call; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.3.call_id").String(); got != "call_1" {
		t.Fatalf("input.3.call_id = %q, want call_1; body=%s", got, string(secondBody))
	}
}

func TestCodexExecutorReasoningReplayCacheUsesExistingToolCallAsReplayAnchor(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	encryptedContent := validCodexReasoningEncryptedContentForTestSeed(15)
	scope := codexReasoningReplayScope{
		modelName:  "gpt-5.4",
		sessionKey: "claude:session-existing-tool",
	}
	cacheCodexReasoningReplayFromCompleted(scope, []byte(`{"response":{"output":[`+
		`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+encryptedContent+`"},`+
		`{"type":"function_call","call_id":"call_existing","name":"lookup","arguments":"{}"}`+
		`]}}`))

	body := []byte(`{"model":"gpt-5.4","input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},` +
		`{"type":"function_call","call_id":"call_existing","name":"lookup","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_existing","output":"sunny"}` +
		`]}`)
	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-existing-tool\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}

	updated, replayScope := applyCodexReasoningReplayCache(
		context.Background(),
		sdktranslator.FromString("claude"),
		req,
		cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")},
		body,
	)
	if replayScope != scope {
		t.Fatalf("replay scope = %#v, want %#v", replayScope, scope)
	}
	if got := gjson.GetBytes(updated, "input.0.role").String(); got != "user" {
		t.Fatalf("input.0.role = %q, want user; body=%s", got, string(updated))
	}
	if got := gjson.GetBytes(updated, "input.1.type").String(); got != "reasoning" {
		t.Fatalf("input.1.type = %q, want reasoning before existing tool call; body=%s", got, string(updated))
	}
	if got := gjson.GetBytes(updated, "input.1.encrypted_content").String(); got != encryptedContent {
		t.Fatalf("input.1.encrypted_content = %q, want %q; body=%s", got, encryptedContent, string(updated))
	}
	if got := gjson.GetBytes(updated, "input.2.type").String(); got != "function_call" {
		t.Fatalf("input.2.type = %q, want existing function_call; body=%s", got, string(updated))
	}
	if got := gjson.GetBytes(updated, "input.3.type").String(); got != "function_call_output" {
		t.Fatalf("input.3.type = %q, want function_call_output; body=%s", got, string(updated))
	}

	functionCallCount := 0
	for _, item := range gjson.GetBytes(updated, "input").Array() {
		if item.Get("type").String() == "function_call" && item.Get("call_id").String() == "call_existing" {
			functionCallCount++
		}
	}
	if functionCallCount != 1 {
		t.Fatalf("function_call count = %d, want 1 without a duplicate replay item; body=%s", functionCallCount, string(updated))
	}
}

func TestCodexExecutorReasoningReplayCachePreservesEarlierToolTurnAnchors(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	firstEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(21)
	secondEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(22)
	scope := codexReasoningReplayScope{
		modelName:  "gpt-5.4",
		sessionKey: "claude:session-multiple-tool-turns",
	}
	cacheCodexReasoningReplayFromCompleted(scope, []byte(`{"response":{"output":[`+
		`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+firstEncryptedContent+`"},`+
		`{"type":"function_call","call_id":"call_first","name":"lookup","arguments":"{}"}`+
		`]}}`))
	cacheCodexReasoningReplayFromCompleted(scope, []byte(`{"response":{"output":[`+
		`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+secondEncryptedContent+`"},`+
		`{"type":"function_call","call_id":"call_second","name":"read","arguments":"{}"}`+
		`]}}`))

	cachedItems, ok := internalcache.GetCodexReasoningReplayItems(scope.modelName, scope.sessionKey)
	if !ok {
		t.Fatal("expected accumulated reasoning replay cache entry")
	}
	if got := len(cachedItems); got != 4 {
		t.Fatalf("cached item count = %d, want 4", got)
	}

	body := []byte(`{"model":"gpt-5.4","input":[` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},` +
		`{"type":"function_call","call_id":"call_first","name":"lookup","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_first","output":"one"},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"second"}]},` +
		`{"type":"function_call","call_id":"call_second","name":"read","arguments":"{}"},` +
		`{"type":"function_call_output","call_id":"call_second","output":"two"}` +
		`]}`)
	replayItems := filterCodexReasoningReplayItemsForInput(body, cachedItems)
	updated, inserted := insertCodexReasoningReplayItems(body, replayItems)
	if !inserted {
		t.Fatal("expected accumulated reasoning items to be inserted")
	}

	input := gjson.GetBytes(updated, "input").Array()
	if got := len(input); got != 8 {
		t.Fatalf("input item count = %d, want 8; body=%s", got, string(updated))
	}
	if got := input[1].Get("encrypted_content").String(); got != firstEncryptedContent {
		t.Fatalf("input.1 encrypted_content = %q, want first turn reasoning", got)
	}
	if got := input[2].Get("call_id").String(); got != "call_first" {
		t.Fatalf("input.2 call_id = %q, want call_first; body=%s", got, string(updated))
	}
	if got := input[5].Get("encrypted_content").String(); got != secondEncryptedContent {
		t.Fatalf("input.5 encrypted_content = %q, want second turn reasoning", got)
	}
	if got := input[6].Get("call_id").String(); got != "call_second" {
		t.Fatalf("input.6 call_id = %q, want call_second; body=%s", got, string(updated))
	}
}

func TestCodexExecutorReasoningReplayCacheDropsFunctionCallWithoutMatchingOutput(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	encryptedContent := validCodexReasoningEncryptedContentForTestSeed(14)
	scope := codexReasoningReplayScope{
		modelName:  "gpt-5.4",
		sessionKey: "claude:session-dropped-tool",
	}
	cacheCodexReasoningReplayFromCompleted(scope, []byte(`{"response":{"output":[`+
		`{"type":"reasoning","summary":[],"content":null,"encrypted_content":"`+encryptedContent+`"},`+
		`{"type":"function_call","call_id":"call_dropped","name":"TaskCreate","arguments":"{}"}`+
		`]}}`))

	body := []byte(`{"model":"gpt-5.4","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}]}`)
	req := cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"session-dropped-tool\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"next"}]}]
		}`),
	}

	updated, replayScope := applyCodexReasoningReplayCache(
		context.Background(),
		sdktranslator.FromString("claude"),
		req,
		cliproxyexecutor.Options{SourceFormat: sdktranslator.FromString("claude")},
		body,
	)
	if replayScope != scope {
		t.Fatalf("replay scope = %#v, want %#v", replayScope, scope)
	}
	if got := gjson.GetBytes(updated, "input.0.type").String(); got != "reasoning" {
		t.Fatalf("input.0.type = %q, want reasoning; body=%s", got, string(updated))
	}
	if got := gjson.GetBytes(updated, "input.0.encrypted_content").String(); got != encryptedContent {
		t.Fatalf("input.0.encrypted_content = %q, want cached reasoning; body=%s", got, string(updated))
	}
	if gjson.GetBytes(updated, `input.#(call_id=="call_dropped")`).Exists() {
		t.Fatalf("cached function_call without matching output should not be replayed; body=%s", string(updated))
	}
	if got := gjson.GetBytes(updated, "input.1.role").String(); got != "user" {
		t.Fatalf("input.1.role = %q, want user; body=%s", got, string(updated))
	}
}

func TestCodexExecutorReasoningReplayCacheMatchesShortenedClaudeToolResultCallID(t *testing.T) {
	internalcache.ClearCodexReasoningReplayCache()
	t.Cleanup(internalcache.ClearCodexReasoningReplayCache)

	longCallID := "call_" + strings.Repeat("a", 62)
	shortCallID := shortenedCodexReplayCallIDForTest(longCallID)
	if len(longCallID) <= 64 || len(shortCallID) > 64 || shortCallID == longCallID {
		t.Fatalf("invalid test setup: long=%q short=%q", longCallID, shortCallID)
	}

	reasoningEncryptedContent := validCodexReasoningEncryptedContentForTestSeed(13)
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, errRead := io.ReadAll(r.Body)
		if errRead != nil {
			t.Fatalf("read body: %v", errRead)
		}
		bodies = append(bodies, body)

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"rs_long","type":"reasoning","summary":[],"encrypted_content":"` + reasoningEncryptedContent + `"},"output_index":0}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"fc_long","type":"function_call","call_id":"` + longCallID + `","name":"lookup","arguments":"{\"q\":\"weather\"}","status":"completed"},"output_index":1}` + "\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"gpt-5.4","output":[]}}` + "\n\n"))
	}))
	defer server.Close()

	executor := NewCodexExecutor(&config.Config{})
	auth := &cliproxyauth.Auth{
		ID: "auth-replay-claude-short-tool",
		Attributes: map[string]string{
			"base_url": server.URL,
			"api_key":  "test",
		},
	}
	opts := cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("claude"),
		Stream:       false,
	}

	_, err := executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"claude-session-short-tool\"}"},
			"messages":[{"role":"user","content":[{"type":"text","text":"call lookup"}]}],
			"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]
		}`),
	}, opts)
	if err != nil {
		t.Fatalf("first Execute error: %v", err)
	}

	_, err = executor.Execute(context.Background(), auth, cliproxyexecutor.Request{
		Model: "gpt-5.4",
		Payload: []byte(`{
			"model":"gpt-5.4",
			"metadata":{"user_id":"{\"device_id\":\"device-test\",\"account_uuid\":\"\",\"session_id\":\"claude-session-short-tool\"}"},
			"messages":[
				{"role":"user","content":[{"type":"text","text":"call lookup"}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + shortCallID + `","content":"sunny"}]}
			],
			"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{"q":{"type":"string"}}}}]
		}`),
	}, opts)
	if err != nil {
		t.Fatalf("second Execute error: %v", err)
	}

	if len(bodies) != 2 {
		t.Fatalf("upstream request count = %d, want 2", len(bodies))
	}
	secondBody := bodies[1]
	if got := gjson.GetBytes(secondBody, "input.0.type").String(); got != "message" {
		t.Fatalf("input.0.type = %q, want initial user message; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.1.type").String(); got != "reasoning" {
		t.Fatalf("input.1.type = %q, want cached reasoning; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.2.type").String(); got != "function_call" {
		t.Fatalf("input.2.type = %q, want cached function_call; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.2.call_id").String(); got != shortCallID {
		t.Fatalf("input.2.call_id = %q, want shortened call_id %q; body=%s", got, shortCallID, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.3.type").String(); got != "function_call_output" {
		t.Fatalf("input.3.type = %q, want function_call_output after cached call; body=%s", got, string(secondBody))
	}
	if got := gjson.GetBytes(secondBody, "input.3.call_id").String(); got != shortCallID {
		t.Fatalf("input.3.call_id = %q, want shortened call_id %q; body=%s", got, shortCallID, string(secondBody))
	}
}
