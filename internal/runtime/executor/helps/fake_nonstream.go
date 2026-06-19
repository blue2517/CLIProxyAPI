package helps

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// fakeNonStreamDataTag is the SSE field prefix carrying JSON payloads.
var fakeNonStreamDataTag = []byte("data:")

// sseDataPayloads extracts the JSON payloads from the "data:" lines of an SSE
// stream, dropping event lines, comments, and the "[DONE]" sentinel.
func sseDataPayloads(stream []byte) [][]byte {
	lines := bytes.Split(stream, []byte("\n"))
	payloads := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if !bytes.HasPrefix(line, fakeNonStreamDataTag) {
			continue
		}
		payload := bytes.TrimSpace(line[len(fakeNonStreamDataTag):])
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

// claudeBlockAccumulator collects the streamed pieces of a single Claude content
// block (text, thinking, or tool_use) so the block can be rebuilt verbatim.
type claudeBlockAccumulator struct {
	block       []byte
	blockType   string
	text        strings.Builder
	thinking    strings.Builder
	signature   string
	partialJSON strings.Builder
}

// AggregateClaudeSSEToMessages reconstructs a single Claude Messages API
// non-streaming response (a {"type":"message",...} object) from a Claude SSE
// stream. It is used by the fake-non-stream path, where a client non-streaming
// request is issued to the upstream as streaming for reliability and then
// collapsed back into the shape the client expects.
//
// When the stream cannot be parsed as Claude SSE events (e.g. the upstream
// already returned a plain JSON body), the original bytes are returned unchanged.
func AggregateClaudeSSEToMessages(stream []byte) []byte {
	payloads := sseDataPayloads(stream)
	if len(payloads) == 0 {
		return stream
	}

	var message []byte
	blocks := make(map[int]*claudeBlockAccumulator)
	maxIndex := -1
	sawMessageStart := false

	for _, payload := range payloads {
		root := gjson.ParseBytes(payload)
		switch root.Get("type").String() {
		case "message_start":
			if msg := root.Get("message"); msg.Exists() {
				message = []byte(msg.Raw)
				// Clear the streamed-empty content; rebuilt below.
				message, _ = sjson.SetRawBytes(message, "content", []byte("[]"))
				sawMessageStart = true
			}

		case "content_block_start":
			index := int(root.Get("index").Int())
			cb := root.Get("content_block")
			acc := &claudeBlockAccumulator{}
			if cb.Exists() {
				acc.block = []byte(cb.Raw)
				acc.blockType = cb.Get("type").String()
			}
			blocks[index] = acc
			if index > maxIndex {
				maxIndex = index
			}

		case "content_block_delta":
			index := int(root.Get("index").Int())
			acc := blocks[index]
			if acc == nil {
				acc = &claudeBlockAccumulator{}
				blocks[index] = acc
				if index > maxIndex {
					maxIndex = index
				}
			}
			delta := root.Get("delta")
			switch delta.Get("type").String() {
			case "text_delta":
				acc.text.WriteString(delta.Get("text").String())
			case "thinking_delta":
				acc.thinking.WriteString(delta.Get("thinking").String())
			case "signature_delta":
				if sig := delta.Get("signature"); sig.Exists() {
					acc.signature = sig.String()
				}
			case "input_json_delta":
				acc.partialJSON.WriteString(delta.Get("partial_json").String())
			}

		case "message_delta":
			if delta := root.Get("delta"); delta.Exists() {
				if sr := delta.Get("stop_reason"); sr.Exists() && len(message) > 0 {
					message, _ = sjson.SetBytes(message, "stop_reason", sr.Value())
				}
				if ss := delta.Get("stop_sequence"); ss.Exists() && len(message) > 0 {
					message, _ = sjson.SetBytes(message, "stop_sequence", ss.Value())
				}
			}
			if usage := root.Get("usage"); usage.Exists() && len(message) > 0 {
				usage.ForEach(func(key, value gjson.Result) bool {
					message, _ = sjson.SetBytes(message, "usage."+key.String(), value.Value())
					return true
				})
			}
		}
	}

	if !sawMessageStart || len(message) == 0 {
		return stream
	}

	// Rebuild the content array in index order from the accumulated blocks.
	content := []byte("[]")
	for i := 0; i <= maxIndex; i++ {
		acc := blocks[i]
		if acc == nil {
			continue
		}
		block := acc.block
		if len(block) == 0 {
			block = []byte("{}")
		}
		switch acc.blockType {
		case "text":
			block, _ = sjson.SetBytes(block, "text", acc.text.String())
		case "thinking":
			block, _ = sjson.SetBytes(block, "thinking", acc.thinking.String())
			if acc.signature != "" {
				block, _ = sjson.SetBytes(block, "signature", acc.signature)
			}
		case "tool_use":
			raw := acc.partialJSON.String()
			if strings.TrimSpace(raw) == "" {
				raw = "{}"
			}
			if gjson.Valid(raw) {
				block, _ = sjson.SetRawBytes(block, "input", []byte(raw))
			} else {
				block, _ = sjson.SetBytes(block, "input", raw)
			}
		}
		content, _ = sjson.SetRawBytes(content, "-1", block)
	}
	message, _ = sjson.SetRawBytes(message, "content", content)
	return message
}

// openAIToolCallAccumulator collects streamed tool_call deltas for a single
// choice so the final non-streaming message can carry complete tool calls.
type openAIToolCallAccumulator struct {
	id        string
	callType  string
	name      string
	arguments strings.Builder
}

// AggregateOpenAISSEToChatCompletion reconstructs a single OpenAI
// chat.completion object from an OpenAI Chat Completions SSE stream
// (chat.completion.chunk deltas). It backs the fake-non-stream path for
// OpenAI-compatible upstreams.
//
// When the stream cannot be parsed as OpenAI SSE chunks, the original bytes are
// returned unchanged.
func AggregateOpenAISSEToChatCompletion(stream []byte) []byte {
	payloads := sseDataPayloads(stream)
	if len(payloads) == 0 {
		return stream
	}

	out := []byte(`{"id":"","object":"chat.completion","created":0,"model":"","choices":[{"index":0,"message":{"role":"assistant","content":""},"finish_reason":null}]}`)

	var (
		sawChunk     bool
		content      strings.Builder
		reasoning    strings.Builder
		role         string
		finishReason string
		usageRaw     string
	)
	toolCalls := make(map[int]*openAIToolCallAccumulator)
	maxToolIndex := -1

	for _, payload := range payloads {
		root := gjson.ParseBytes(payload)
		if root.Get("object").String() == "chat.completion.chunk" || root.Get("choices").Exists() {
			sawChunk = true
		}
		if id := root.Get("id"); id.Exists() && id.String() != "" {
			out, _ = sjson.SetBytes(out, "id", id.String())
		}
		if created := root.Get("created"); created.Exists() {
			out, _ = sjson.SetBytes(out, "created", created.Int())
		}
		if model := root.Get("model"); model.Exists() && model.String() != "" {
			out, _ = sjson.SetBytes(out, "model", model.String())
		}
		if usage := root.Get("usage"); usage.Exists() && usage.Type != gjson.Null {
			usageRaw = usage.Raw
		}

		choice := root.Get("choices.0")
		if !choice.Exists() {
			continue
		}
		if fr := choice.Get("finish_reason"); fr.Exists() && fr.String() != "" {
			finishReason = fr.String()
		}
		delta := choice.Get("delta")
		if !delta.Exists() {
			continue
		}
		if r := delta.Get("role"); r.Exists() && r.String() != "" {
			role = r.String()
		}
		if c := delta.Get("content"); c.Exists() && c.Type == gjson.String {
			content.WriteString(c.String())
		}
		if rc := delta.Get("reasoning_content"); rc.Exists() && rc.Type == gjson.String {
			reasoning.WriteString(rc.String())
		}
		if r := delta.Get("reasoning"); r.Exists() && r.Type == gjson.String {
			reasoning.WriteString(r.String())
		}
		delta.Get("tool_calls").ForEach(func(_, tc gjson.Result) bool {
			index := int(tc.Get("index").Int())
			acc := toolCalls[index]
			if acc == nil {
				acc = &openAIToolCallAccumulator{}
				toolCalls[index] = acc
				if index > maxToolIndex {
					maxToolIndex = index
				}
			}
			if id := tc.Get("id"); id.Exists() && id.String() != "" {
				acc.id = id.String()
			}
			if t := tc.Get("type"); t.Exists() && t.String() != "" {
				acc.callType = t.String()
			}
			if name := tc.Get("function.name"); name.Exists() && name.String() != "" {
				acc.name = name.String()
			}
			acc.arguments.WriteString(tc.Get("function.arguments").String())
			return true
		})
	}

	if !sawChunk {
		return stream
	}

	if role == "" {
		role = "assistant"
	}
	out, _ = sjson.SetBytes(out, "choices.0.message.role", role)
	out, _ = sjson.SetBytes(out, "choices.0.message.content", content.String())
	if reasoning.Len() > 0 {
		out, _ = sjson.SetBytes(out, "choices.0.message.reasoning_content", reasoning.String())
	}
	if maxToolIndex >= 0 {
		toolArray := []byte("[]")
		for i := 0; i <= maxToolIndex; i++ {
			acc := toolCalls[i]
			if acc == nil {
				continue
			}
			callType := acc.callType
			if callType == "" {
				callType = "function"
			}
			args := acc.arguments.String()
			if args == "" {
				args = "{}"
			}
			tc := []byte(`{}`)
			tc, _ = sjson.SetBytes(tc, "id", acc.id)
			tc, _ = sjson.SetBytes(tc, "type", callType)
			tc, _ = sjson.SetBytes(tc, "function.name", acc.name)
			tc, _ = sjson.SetBytes(tc, "function.arguments", args)
			toolArray, _ = sjson.SetRawBytes(toolArray, "-1", tc)
		}
		out, _ = sjson.SetRawBytes(out, "choices.0.message.tool_calls", toolArray)
		// The content field must stay present; OpenAI clients tolerate "".
	}
	if finishReason == "" {
		finishReason = "stop"
	}
	out, _ = sjson.SetBytes(out, "choices.0.finish_reason", finishReason)
	if usageRaw != "" {
		out, _ = sjson.SetRawBytes(out, "usage", []byte(usageRaw))
	}
	return out
}

// AggregateGeminiSSEToGenerateContent reconstructs a single Gemini
// GenerateContentResponse from a streamGenerateContent SSE stream. Streamed text
// parts are concatenated; non-text parts (e.g. functionCall) are preserved in
// order. The final finishReason, usageMetadata, and modelVersion win.
//
// When the stream cannot be parsed as Gemini SSE chunks, the original bytes are
// returned unchanged.
func AggregateGeminiSSEToGenerateContent(stream []byte) []byte {
	payloads := sseDataPayloads(stream)
	if len(payloads) == 0 {
		return stream
	}

	var (
		sawCandidate bool
		role         string
		finishReason string
		usageRaw     string
		modelVersion string
		responseID   string
	)
	parts := make([]string, 0, len(payloads)) // raw JSON of each emitted part
	var pendingText strings.Builder

	flushText := func() {
		if pendingText.Len() == 0 {
			return
		}
		part := []byte(`{}`)
		part, _ = sjson.SetBytes(part, "text", pendingText.String())
		parts = append(parts, string(part))
		pendingText.Reset()
	}

	for _, payload := range payloads {
		root := gjson.ParseBytes(payload)
		candidate := root.Get("candidates.0")
		if candidate.Exists() {
			sawCandidate = true
			if r := candidate.Get("content.role"); r.Exists() && r.String() != "" {
				role = r.String()
			}
			candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
				if text := part.Get("text"); text.Exists() && !part.Get("functionCall").Exists() && !part.Get("inlineData").Exists() {
					pendingText.WriteString(text.String())
					return true
				}
				// Non-text part: flush accumulated text first to keep ordering.
				flushText()
				parts = append(parts, part.Raw)
				return true
			})
			if fr := candidate.Get("finishReason"); fr.Exists() && fr.String() != "" {
				finishReason = fr.String()
			}
		}
		if usage := root.Get("usageMetadata"); usage.Exists() {
			usageRaw = usage.Raw
		}
		if mv := root.Get("modelVersion"); mv.Exists() && mv.String() != "" {
			modelVersion = mv.String()
		}
		if id := root.Get("responseId"); id.Exists() && id.String() != "" {
			responseID = id.String()
		}
	}
	flushText()

	if !sawCandidate {
		return stream
	}
	if role == "" {
		role = "model"
	}

	partsArray := []byte("[]")
	for _, p := range parts {
		partsArray, _ = sjson.SetRawBytes(partsArray, "-1", []byte(p))
	}

	out := []byte(`{"candidates":[{"content":{"parts":[],"role":"model"},"index":0}]}`)
	out, _ = sjson.SetRawBytes(out, "candidates.0.content.parts", partsArray)
	out, _ = sjson.SetBytes(out, "candidates.0.content.role", role)
	if finishReason != "" {
		out, _ = sjson.SetBytes(out, "candidates.0.finishReason", finishReason)
	}
	if usageRaw != "" {
		out, _ = sjson.SetRawBytes(out, "usageMetadata", []byte(usageRaw))
	}
	if modelVersion != "" {
		out, _ = sjson.SetBytes(out, "modelVersion", modelVersion)
	}
	if responseID != "" {
		out, _ = sjson.SetBytes(out, "responseId", responseID)
	}
	return out
}
