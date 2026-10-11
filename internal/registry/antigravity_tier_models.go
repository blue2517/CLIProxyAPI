package registry

import "strings"

// completeAntigravityTierModels fills missing native tiers for the confirmed
// Gemini Flash families. Explicit catalog entries always take precedence.
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
		switch model.ID {
		case "gemini-3.6-flash-high", "gemini-3.7-flash-high", "gemini-3.8-flash-high":
		default:
			continue
		}
		baseID := strings.TrimSuffix(model.ID, "-high")
		for _, tier := range []struct {
			id    string
			label string
		}{
			{id: "medium", label: "Medium"},
			{id: "low", label: "Low"},
		} {
			id := baseID + "-" + tier.id
			if _, exists := seen[id]; exists {
				continue
			}
			variant := cloneModelInfo(model)
			variant.ID = id
			variant.Name = id
			variant.DisplayName = strings.TrimSuffix(model.DisplayName, " (High)") + " (" + tier.label + ")"
			variant.Description = strings.TrimSuffix(model.Description, " (High)") + " (" + tier.label + ")"
			completed = append(completed, variant)
			seen[id] = struct{}{}
		}
	}
	return completed
}
