package registry

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestCompleteAntigravityTierModels(t *testing.T) {
	for _, version := range []string{"3.6", "3.7", "3.8"} {
		t.Run(version, func(t *testing.T) {
			baseID := "gemini-" + version + "-flash"
			displayName := "Gemini " + version + " Flash"
			high := &ModelInfo{
				ID: baseID + "-high", Name: baseID + "-high", DisplayName: displayName,
				Description: displayName + " (High)", Type: "antigravity", OwnedBy: "antigravity",
				ContextLength: 1048576, MaxCompletionTokens: 65536, SupportsWebSearch: true,
				Thinking:                 &ThinkingSupport{Min: 1, Max: 65535, DynamicAllowed: true, Levels: []string{"low", "medium", "high"}},
				SupportedInputModalities: []string{"text", "image"},
				Config:                   &ModelConfig{OverrideHeader: map[string]string{"user-agent": "test-agent"}},
			}
			original := cloneModelInfo(high)
			models := completeAntigravityTierModels([]*ModelInfo{high})
			if len(models) != 3 || models[0] != high {
				t.Fatalf("models = %+v, want original high and two native tiers", models)
			}
			for i, tier := range []string{"medium", "low"} {
				variant := models[i+1]
				label := []string{"Medium", "Low"}[i]
				if variant.ID != baseID+"-"+tier || variant.Name != variant.ID ||
					variant.DisplayName != displayName+" ("+label+")" || variant.Description != variant.DisplayName {
					t.Fatalf("incorrect tier identity: %+v", variant)
				}
				metadata := cloneModelInfo(variant)
				metadata.ID, metadata.Name = high.ID, high.Name
				metadata.DisplayName, metadata.Description = high.DisplayName, high.Description
				if !reflect.DeepEqual(metadata, high) {
					t.Fatalf("tier lost family metadata: %+v", variant)
				}
			}
			models[1].Thinking.Levels[0] = "changed"
			models[1].SupportedInputModalities[0] = "changed"
			models[1].Config.OverrideHeader["user-agent"] = "changed"
			if !reflect.DeepEqual(high, original) || !reflect.DeepEqual(models[2].Thinking, original.Thinking) ||
				!reflect.DeepEqual(models[2].SupportedInputModalities, original.SupportedInputModalities) ||
				!reflect.DeepEqual(models[2].Config, original.Config) {
				t.Fatal("tier metadata shares mutable state")
			}
		})
	}
}

func TestCompleteAntigravityTierModelsPreservesExplicitEntries(t *testing.T) {
	high := &ModelInfo{ID: "gemini-3.8-flash-high", DisplayName: "Gemini 3.8 Flash (High)"}
	medium := &ModelInfo{ID: "gemini-3.8-flash-medium", DisplayName: "Custom medium", ContextLength: 12345}
	models := completeAntigravityTierModels([]*ModelInfo{high, medium})
	if len(models) != 3 || models[1] != medium || models[2].ID != "gemini-3.8-flash-low" {
		t.Fatalf("explicit medium entry was replaced: %+v", models)
	}
	if models[2].DisplayName != "Gemini 3.8 Flash (Low)" {
		t.Fatalf("low display name = %q", models[2].DisplayName)
	}
	if again := completeAntigravityTierModels(models); !reflect.DeepEqual(again, models) {
		t.Fatalf("completion is not idempotent: %+v", again)
	}
	low := &ModelInfo{ID: "gemini-3.8-flash-low", DisplayName: "Custom low", ContextLength: 54321}
	explicit := []*ModelInfo{high, medium, low}
	if got := completeAntigravityTierModels(explicit); !reflect.DeepEqual(got, explicit) || got[2] != low {
		t.Fatalf("explicit low entry was replaced: %+v", got)
	}
}

func TestCompleteAntigravityTierModelsLeavesOtherModelsUnchanged(t *testing.T) {
	if completeAntigravityTierModels(nil) != nil {
		t.Fatal("nil catalog changed")
	}
	models := []*ModelInfo{
		nil,
		{ID: "gemini-3-flash"},
		{ID: "gemini-3.8-flash-medium"},
		{ID: "gemini-3.8-flash-tiered"},
		{ID: "gemini-3.9-flash-high"},
		{ID: "gemini-3.1-pro-high"},
		{ID: "claude-sonnet-5-5-high"},
	}
	if got := completeAntigravityTierModels(models); !reflect.DeepEqual(got, models) {
		t.Fatalf("unconfirmed model tiers were added: %+v", got)
	}
}

func TestAntigravityTierModelsCatalogLoadingAndRefresh(t *testing.T) {
	original := getModels()
	t.Cleanup(func() {
		modelsCatalogStore.mu.Lock()
		modelsCatalogStore.data = original
		modelsCatalogStore.mu.Unlock()
	})
	if errLoad := loadModelsFromBytes(embeddedModelsJSON, "test"); errLoad != nil {
		t.Fatal(errLoad)
	}
	for _, version := range []string{"3.6", "3.7", "3.8"} {
		for _, tier := range []string{"high", "medium", "low"} {
			id := "gemini-" + version + "-flash-" + tier
			if LookupStaticModelInfoByChannel(id, "antigravity") == nil || LookupStaticModelInfo(id) == nil {
				t.Fatalf("embedded catalog lookup is missing %s", id)
			}
		}
	}
	var raw map[string]json.RawMessage
	if errUnmarshal := json.Unmarshal(embeddedModelsJSON, &raw); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	raw["antigravity"] = json.RawMessage(`[{"id":"gemini-3.8-flash-high","display_name":"Gemini 3.8 Flash","context_length":2000000,"thinking":{"levels":["low","medium","high"]}},{"id":"gemini-3.8-flash-medium","display_name":"Explicit medium","context_length":12345}]`)
	refresh, errMarshal := json.Marshal(raw)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	changed, errPublish := publishCatalogBytes(refresh)
	if errPublish != nil {
		t.Fatal(errPublish)
	}
	if !slices.Contains(changed, "antigravity") {
		t.Fatalf("tier catalog refresh did not report antigravity: %v", changed)
	}
	if medium := LookupStaticModelInfo("gemini-3.8-flash-medium"); medium == nil || medium.DisplayName != "Explicit medium" || medium.ContextLength != 12345 {
		t.Fatalf("explicit refreshed medium lost: %+v", medium)
	}
	if low := LookupStaticModelInfo("gemini-3.8-flash-low"); low == nil || low.ContextLength != 2000000 || low.Thinking == nil || len(low.Thinking.Levels) != 3 {
		t.Fatalf("refreshed low did not inherit current metadata: %+v", low)
	}
	if len(GetAntigravityModels()) != 3 || LookupStaticModelInfo("gemini-3.7-flash-medium") != nil {
		t.Fatal("removed family was restored during refresh")
	}
	if changed, errPublish = publishCatalogBytes(refresh); errPublish != nil || len(changed) != 0 {
		t.Fatalf("identical refresh reported changes: %v, %v", changed, errPublish)
	}
	raw["antigravity"] = json.RawMessage(`[]`)
	empty, errMarshal := json.Marshal(raw)
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	if _, errPublish = publishCatalogBytes(empty); errPublish != nil {
		t.Fatal(errPublish)
	}
	if len(GetAntigravityModels()) != 0 {
		t.Fatal("empty catalog restored built-in tiers")
	}
}
