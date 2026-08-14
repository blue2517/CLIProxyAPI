package thinking

import (
	"strings"

	"github.com/tidwall/gjson"
)

// ClaudeThinkingIncludesContent reports whether an active Claude thinking
// request should ask upstreams (such as Google or Codex) to return thought text.
// Explicit display="omitted" remains authoritative; otherwise active thinking
// includes its content because upstreams hide it unless includeThoughts or
// reasoning.summary is set.
func ClaudeThinkingIncludesContent(body []byte) (bool, bool) {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return false, false
	}

	thinkingType := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, "thinking.type").String()))
	switch thinkingType {
	case "enabled", "adaptive", "auto":
	default:
		return false, false
	}

	if thinkingType == "enabled" {
		if budget := gjson.GetBytes(body, "thinking.budget_tokens"); budget.Exists() && budget.Type == gjson.Number && budget.Int() == 0 {
			return false, false
		}
	}

	if summary := ExtractSummaryConfig(body, "claude"); summary.Mode == SummaryDisabled {
		return false, true
	}
	return true, true
}
