package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestAntigravityQuotaCooldownBucketsAreIndependent(t *testing.T) {
	m := NewManager(nil, nil, nil)
	auth := &Auth{ID: "ag-buckets", Provider: "antigravity"}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	retryAfter := time.Hour
	m.MarkResult(context.Background(), Result{
		AuthID:     auth.ID,
		Provider:   auth.Provider,
		Model:      "gemini-3-flash",
		Success:    false,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"},
		RetryAfter: &retryAfter,
	})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if blocked, reason, _ := isAuthBlockedForModel(updated, "gemini-2.5-flash", time.Now()); !blocked || reason != blockReasonCooldown {
		t.Fatalf("gemini bucket blocked=%v reason=%v, want cooldown", blocked, reason)
	}
	if blocked, reason, _ := isAuthBlockedForModel(updated, "claude-opus-4-6-thinking", time.Now()); blocked || reason != blockReasonNone {
		t.Fatalf("claude/gpt bucket blocked=%v reason=%v, want ready", blocked, reason)
	}
}

func TestAntigravityQuotaRefreshClearsMatchingBucket(t *testing.T) {
	m := NewManager(nil, nil, nil)
	auth := &Auth{ID: "ag-refresh-clear", Provider: "antigravity"}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	retryAfter := time.Hour
	m.MarkResult(context.Background(), Result{
		AuthID:     auth.ID,
		Provider:   auth.Provider,
		Model:      "gemini-3-flash",
		Success:    false,
		Error:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: "quota"},
		RetryAfter: &retryAfter,
	})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	refreshed := updated.Clone()
	if refreshed.Metadata == nil {
		refreshed.Metadata = make(map[string]any)
	}
	refreshed.Metadata[AntigravityCooldownClearMetadata] = []string{AntigravityGeminiQuotaBucket}
	if _, errUpdate := m.Update(context.Background(), refreshed); errUpdate != nil {
		t.Fatalf("update auth: %v", errUpdate)
	}

	cleared, ok := m.GetByID(auth.ID)
	if !ok || cleared == nil {
		t.Fatal("expected auth to be present after update")
	}
	if blocked, reason, _ := isAuthBlockedForModel(cleared, "gemini-2.5-flash", time.Now()); blocked || reason != blockReasonNone {
		t.Fatalf("gemini bucket blocked=%v reason=%v, want cleared", blocked, reason)
	}
	if _, exists := cleared.Metadata[AntigravityCooldownClearMetadata]; exists {
		t.Fatal("cooldown clear metadata should be consumed")
	}
}
