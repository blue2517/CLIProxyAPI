package helps

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestAggregateClaudeSSEToMessages_TextAndUsage(t *testing.T) {
	stream := "" +
		"event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-4-8","content":[],"stop_reason":null,"usage":{"input_tokens":10,"output_tokens":1}}}` + "\n\n" +
		"event: content_block_start\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}` + "\n\n" +
		"event: content_block_stop\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}` + "\n\n"

	out := AggregateClaudeSSEToMessages([]byte(stream))
	root := gjson.ParseBytes(out)

	if got := root.Get("type").String(); got != "message" {
		t.Fatalf("type = %q, want message", got)
	}
	if got := root.Get("id").String(); got != "msg_1" {
		t.Fatalf("id = %q, want msg_1", got)
	}
	if got := root.Get("content.0.type").String(); got != "text" {
		t.Fatalf("content.0.type = %q, want text", got)
	}
	if got := root.Get("content.0.text").String(); got != "Hello" {
		t.Fatalf("content.0.text = %q, want Hello", got)
	}
	if got := root.Get("stop_reason").String(); got != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn", got)
	}
	if got := root.Get("usage.input_tokens").Int(); got != 10 {
		t.Fatalf("usage.input_tokens = %d, want 10", got)
	}
	if got := root.Get("usage.output_tokens").Int(); got != 5 {
		t.Fatalf("usage.output_tokens = %d, want 5", got)
	}
}

func TestAggregateClaudeSSEToMessages_ToolUse(t *testing.T) {
	stream := "" +
		`data: {"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"m","content":[]}}` + "\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tool_1","name":"get_weather","input":{}}}` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}` + "\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"SF\"}"}}` + "\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"}}` + "\n"

	out := AggregateClaudeSSEToMessages([]byte(stream))
	root := gjson.ParseBytes(out)

	if got := root.Get("content.0.type").String(); got != "tool_use" {
		t.Fatalf("content.0.type = %q, want tool_use", got)
	}
	if got := root.Get("content.0.name").String(); got != "get_weather" {
		t.Fatalf("content.0.name = %q, want get_weather", got)
	}
	if got := root.Get("content.0.input.city").String(); got != "SF" {
		t.Fatalf("content.0.input.city = %q, want SF", got)
	}
}

func TestAggregateClaudeSSEToMessages_PassthroughNonSSE(t *testing.T) {
	body := []byte(`{"type":"message","id":"msg_x","content":[{"type":"text","text":"hi"}]}`)
	out := AggregateClaudeSSEToMessages(body)
	if string(out) != string(body) {
		t.Fatalf("expected passthrough, got %s", out)
	}
}

func TestAggregateOpenAISSEToChatCompletion_Text(t *testing.T) {
	stream := "" +
		`data: {"id":"cmpl_1","object":"chat.completion.chunk","created":123,"model":"gpt","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}` + "\n" +
		`data: {"id":"cmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"lo"}}]}` + "\n" +
		`data: {"id":"cmpl_1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}` + "\n" +
		"data: [DONE]\n"

	out := AggregateOpenAISSEToChatCompletion([]byte(stream))
	root := gjson.ParseBytes(out)

	if got := root.Get("object").String(); got != "chat.completion" {
		t.Fatalf("object = %q, want chat.completion", got)
	}
	if got := root.Get("id").String(); got != "cmpl_1" {
		t.Fatalf("id = %q, want cmpl_1", got)
	}
	if got := root.Get("choices.0.message.content").String(); got != "Hello" {
		t.Fatalf("content = %q, want Hello", got)
	}
	if got := root.Get("choices.0.finish_reason").String(); got != "stop" {
		t.Fatalf("finish_reason = %q, want stop", got)
	}
	if got := root.Get("usage.total_tokens").Int(); got != 5 {
		t.Fatalf("usage.total_tokens = %d, want 5", got)
	}
}

func TestAggregateOpenAISSEToChatCompletion_ToolCalls(t *testing.T) {
	stream := "" +
		`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"f","arguments":"{\"a\":"}}]}}]}` + "\n" +
		`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}` + "\n" +
		`data: {"id":"c1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n"

	out := AggregateOpenAISSEToChatCompletion([]byte(stream))
	root := gjson.ParseBytes(out)

	if got := root.Get("choices.0.message.tool_calls.0.function.name").String(); got != "f" {
		t.Fatalf("tool name = %q, want f", got)
	}
	if got := root.Get("choices.0.message.tool_calls.0.function.arguments").String(); got != `{"a":1}` {
		t.Fatalf("tool arguments = %q, want {\"a\":1}", got)
	}
	if got := root.Get("choices.0.finish_reason").String(); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
}

func TestAggregateGeminiSSEToGenerateContent_Text(t *testing.T) {
	stream := "" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]},"index":0}],"modelVersion":"gemini-x"}` + "\n" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]},"index":0}]}` + "\n" +
		`data: {"candidates":[{"content":{"role":"model","parts":[]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2,"totalTokenCount":6}}` + "\n"

	out := AggregateGeminiSSEToGenerateContent([]byte(stream))
	root := gjson.ParseBytes(out)

	if got := root.Get("candidates.0.content.parts.0.text").String(); got != "Hello" {
		t.Fatalf("text = %q, want Hello", got)
	}
	if got := root.Get("candidates.0.finishReason").String(); got != "STOP" {
		t.Fatalf("finishReason = %q, want STOP", got)
	}
	if got := root.Get("usageMetadata.totalTokenCount").Int(); got != 6 {
		t.Fatalf("totalTokenCount = %d, want 6", got)
	}
	if got := root.Get("modelVersion").String(); got != "gemini-x" {
		t.Fatalf("modelVersion = %q, want gemini-x", got)
	}
}

func TestAggregateGeminiSSEToGenerateContent_FunctionCallOrdering(t *testing.T) {
	stream := "" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"a"}]}}]}` + "\n" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"f","args":{"x":1}}}]}}]}` + "\n" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"b"}]},"finishReason":"STOP"}]}` + "\n"

	out := AggregateGeminiSSEToGenerateContent([]byte(stream))
	root := gjson.ParseBytes(out)

	parts := root.Get("candidates.0.content.parts").Array()
	if len(parts) != 3 {
		t.Fatalf("parts len = %d, want 3", len(parts))
	}
	if parts[0].Get("text").String() != "a" {
		t.Fatalf("parts[0].text = %q, want a", parts[0].Get("text").String())
	}
	if parts[1].Get("functionCall.name").String() != "f" {
		t.Fatalf("parts[1].functionCall.name = %q, want f", parts[1].Get("functionCall.name").String())
	}
	if parts[2].Get("text").String() != "b" {
		t.Fatalf("parts[2].text = %q, want b", parts[2].Get("text").String())
	}
}
