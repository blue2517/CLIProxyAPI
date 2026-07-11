package helps

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	ClaudeCodeSessionHeader = "X-Claude-Code-Session-Id"
	ClaudeCodeAgentHeader   = "X-Claude-Code-Agent-Id"
)

var claudeCodeSessionSuffixPattern = regexp.MustCompile(`_session_([a-f0-9-]+)$`)

// ExtractClaudeCodeSessionID resolves a Claude Code session ID, preferring X-Claude-Code-Session-Id over payload metadata.
func ExtractClaudeCodeSessionID(ctx context.Context, payload []byte, headers http.Header) string {
	if headers != nil {
		if sessionID := strings.TrimSpace(headers.Get(ClaudeCodeSessionHeader)); sessionID != "" {
			return sessionID
		}
	}
	if ctx != nil {
		if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
			if sessionID := strings.TrimSpace(ginCtx.Request.Header.Get(ClaudeCodeSessionHeader)); sessionID != "" {
				return sessionID
			}
		}
	}
	return extractClaudeCodeSessionIDFromPayload(payload)
}

// ExtractClaudeCodeAgentID resolves the optional Claude Code agent ID.
func ExtractClaudeCodeAgentID(ctx context.Context, headers http.Header) string {
	if headers != nil {
		if agentID := strings.TrimSpace(headers.Get(ClaudeCodeAgentHeader)); agentID != "" {
			return agentID
		}
	}
	if ctx != nil {
		if ginCtx, ok := ctx.Value("gin").(*gin.Context); ok && ginCtx != nil && ginCtx.Request != nil {
			return strings.TrimSpace(ginCtx.Request.Header.Get(ClaudeCodeAgentHeader))
		}
	}
	return ""
}

// ClaudeCodeConversationKey identifies a Claude Code conversation. Sub-agents
// within the same session have independent histories and must not share state.
func ClaudeCodeConversationKey(ctx context.Context, payload []byte, headers http.Header) string {
	sessionID := ExtractClaudeCodeSessionID(ctx, payload, headers)
	if sessionID == "" {
		return ""
	}
	if agentID := ExtractClaudeCodeAgentID(ctx, headers); agentID != "" {
		return sessionID + ":agent:" + agentID
	}
	return sessionID
}

func extractClaudeCodeSessionIDFromPayload(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	userID := gjson.GetBytes(payload, "metadata.user_id").String()
	if userID == "" {
		return ""
	}
	if matches := claudeCodeSessionSuffixPattern.FindStringSubmatch(userID); len(matches) >= 2 {
		return matches[1]
	}
	if len(userID) > 0 && userID[0] == '{' {
		return strings.TrimSpace(gjson.Get(userID, "session_id").String())
	}
	return ""
}

// ClaudeCodePromptCache maps a Claude Code conversation to a stable upstream prompt_cache_key.
func ClaudeCodePromptCache(ctx context.Context, modelName string, payload []byte, headers http.Header) (CodexCache, bool, error) {
	conversationKey := ClaudeCodeConversationKey(ctx, payload, headers)
	if conversationKey == "" {
		return CodexCache{}, false, nil
	}
	key := CodexPromptCacheKey(modelName, "claude:"+conversationKey)
	if cache, ok, errCache := GetCodexCacheRequired(ctx, key); errCache != nil || ok {
		return cache, ok, errCache
	}
	cache := CodexCache{
		ID:     uuid.New().String(),
		Expire: time.Now().Add(1 * time.Hour),
	}
	if errSet := SetCodexCacheRequired(ctx, key, cache); errSet != nil {
		return CodexCache{}, false, errSet
	}
	return cache, true, nil
}
