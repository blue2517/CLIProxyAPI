package auth

import "strings"

const (
	AntigravityClaudeGPTQuotaBucket  = "__antigravity_quota_claude_gpt__"
	AntigravityGeminiQuotaBucket     = "__antigravity_quota_gemini__"
	AntigravityCooldownClearMetadata = "__antigravity_cooldown_clear_buckets"
)

// AntigravityQuotaBucketForModel returns the shared quota bucket used by
// Antigravity for model cooldowns. Claude/GPT-like models share one bucket;
// Gemini models share a separate bucket.
func AntigravityQuotaBucketForModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(canonicalModelKey(model)))
	if lower == "" {
		return ""
	}
	if strings.HasPrefix(lower, "gemini-") || strings.Contains(lower, "gemini") {
		return AntigravityGeminiQuotaBucket
	}
	return AntigravityClaudeGPTQuotaBucket
}

func antigravityModelKeysForCooldown(model string) []string {
	model = strings.TrimSpace(canonicalModelKey(model))
	if model == "" {
		return nil
	}
	bucket := AntigravityQuotaBucketForModel(model)
	if bucket == "" || bucket == model {
		return []string{model}
	}
	return []string{model, bucket}
}

func antigravityBucketMatchesModel(bucket, model string) bool {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return false
	}
	return bucket == strings.TrimSpace(canonicalModelKey(model)) || bucket == AntigravityQuotaBucketForModel(model)
}

func metadataStringSlice(meta map[string]any, key string) []string {
	if len(meta) == 0 || key == "" {
		return nil
	}
	value, ok := meta[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	default:
		return nil
	}
}
