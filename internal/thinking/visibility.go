package thinking

import (
	"strings"

	"github.com/tidwall/gjson"
)

// ClaudeThinkingIncludesContent reports whether an active Claude thinking
// request should ask Google-compatible upstreams to return thought text.
// Explicit display="omitted" remains authoritative; otherwise active thinking
// includes its content because Google hides it unless includeThoughts is true.
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
