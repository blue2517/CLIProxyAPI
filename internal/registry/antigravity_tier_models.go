package registry

import (
	"slices"
	"strings"
)

type antigravityTierSpec struct {
	id    string
	label string
}

var antigravityTierSpecs = []antigravityTierSpec{
	{id: "xhigh", label: "XHigh"},
	{id: "high", label: "High"},
	{id: "medium", label: "Medium"},
	{id: "low", label: "Low"},
}

// completeAntigravityTierModels fills missing native tier variants for tiered
// Antigravity models based on their supported Thinking.Levels.
// Explicit catalog entries always take precedence.
func completeAntigravityTierModels(models []*ModelInfo) []*ModelInfo {
	if len(models) == 0 {
		return models
	}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model != nil {
			seen[model.ID] = struct{}{}
		}
	}
	completed := append([]*ModelInfo(nil), models...)
	for _, model := range models {
		if model == nil {
			continue
		}
		baseID, ok := antigravityTierBaseID(model.ID)
		if !ok {
			continue
		}
		levels := antigravitySupportedTiers(model)
		if len(levels) == 0 {
			continue
		}
		displayPrefix := stripAntigravityTierLabel(model.DisplayName)
		descPrefix := stripAntigravityTierLabel(model.Description)
		for _, tier := range antigravityTierSpecs {
			if !hasAntigravityTier(levels, tier.id) {
				continue
			}
			id := baseID + "-" + tier.id
			if _, exists := seen[id]; exists {
				continue
			}
			variant := cloneModelInfo(model)
			variant.ID = id
			variant.Name = id
			variant.DisplayName = formatAntigravityTierLabel(displayPrefix, tier.label)
			variant.Description = formatAntigravityTierLabel(descPrefix, tier.label)
			completed = append(completed, variant)
			seen[id] = struct{}{}
		}
	}
	return completed
}

func antigravityTierBaseID(modelID string) (string, bool) {
	for _, tier := range antigravityTierSpecs {
		suffix := "-" + tier.id
		if strings.HasSuffix(modelID, suffix) {
			return strings.TrimSuffix(modelID, suffix), true
		}
	}
	return "", false
}

func antigravitySupportedTiers(model *ModelInfo) []string {
	if model.Thinking != nil && len(model.Thinking.Levels) > 0 {
		return model.Thinking.Levels
	}
	if model.ID == "gemini-3.8-flash-high" {
		return []string{"low", "medium", "high"}
	}
	return nil
}

func hasAntigravityTier(levels []string, target string) bool {
	return slices.ContainsFunc(levels, func(level string) bool {
		return strings.EqualFold(strings.TrimSpace(level), target)
	})
}

func stripAntigravityTierLabel(value string) string {
	for _, tier := range antigravityTierSpecs {
		suffix := " (" + tier.label + ")"
		if strings.HasSuffix(value, suffix) {
			return strings.TrimSuffix(value, suffix)
		}
	}
	return value
}

func formatAntigravityTierLabel(prefix, label string) string {
	if prefix == "" {
		return " (" + label + ")"
	}
	return prefix + " (" + label + ")"
}
