package auth

import (
	"net/http"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestCountTokensPolicyForAuth(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetConfig(&internalconfig.Config{
		ClaudeKey: []internalconfig.ClaudeKey{
			{
				APIKey:      "claude-disabled",
				BaseURL:     "https://disabled.example.com",
				CountTokens: &internalconfig.CountTokensConfig{Mode: internalconfig.CountTokensModeDisabled},
			},
			{
				APIKey:      "claude-redirect",
				BaseURL:     "https://redirect.example.com",
				CountTokens: &internalconfig.CountTokensConfig{Mode: internalconfig.CountTokensModeRedirect, RedirectModel: "gemini-3-pro-preview"},
			},
			{
				APIKey:  "claude-plain",
				BaseURL: "https://plain.example.com",
			},
		},
	})

	cases := []struct {
		name         string
		apiKey       string
		baseURL      string
		wantMode     string
		wantRedirect string
	}{
		{"disabled", "claude-disabled", "https://disabled.example.com", internalconfig.CountTokensModeDisabled, ""},
		{"redirect", "claude-redirect", "https://redirect.example.com", internalconfig.CountTokensModeRedirect, "gemini-3-pro-preview"},
		{"default forward", "claude-plain", "https://plain.example.com", internalconfig.CountTokensModeForward, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := &Auth{
				Provider:   "claude",
				Attributes: map[string]string{"api_key": tc.apiKey, "base_url": tc.baseURL},
			}
			mode, redirect := m.countTokensPolicyForAuth(auth)
			if mode != tc.wantMode {
				t.Fatalf("mode = %q, want %q", mode, tc.wantMode)
			}
			if redirect != tc.wantRedirect {
				t.Fatalf("redirect = %q, want %q", redirect, tc.wantRedirect)
			}
		})
	}
}

func TestCountTokensPolicyDefaultsToForward(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetConfig(&internalconfig.Config{})
	mode, redirect := m.countTokensPolicyForAuth(&Auth{Provider: "claude"})
	if mode != internalconfig.CountTokensModeForward || redirect != "" {
		t.Fatalf("unconfigured auth: mode=%q redirect=%q, want forward/empty", mode, redirect)
	}
}

func TestCountTokensDisabledErrorIsTerminal(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(3, 30*time.Second, 0)

	_, _, maxWait := m.retrySettings()
	wait, shouldRetry := m.shouldRetryAfterError(&countTokensDisabledError{}, 0, []string{"claude"}, "model", maxWait)
	if shouldRetry {
		t.Fatalf("expected disabled count_tokens error to be terminal, got retry (wait=%v)", wait)
	}
	if got := (&countTokensDisabledError{}).StatusCode(); got != http.StatusNotFound {
		t.Fatalf("StatusCode = %d, want %d", got, http.StatusNotFound)
	}
}
