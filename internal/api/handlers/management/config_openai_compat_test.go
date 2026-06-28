package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

func TestGetOpenAICompatIncludesProviderOptions(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			OpenAICompatibility: []config.OpenAICompatibility{{
				Name:           "compat",
				BaseURL:        "https://example.com/v1",
				DisableCooling: true,
				CountTokens:    &config.CountTokensConfig{Mode: config.CountTokensModeDisabled},
				FakeNonStream:  true,
			}},
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/openai-compatibility", nil)

	h.GetOpenAICompat(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body struct {
		OpenAICompatibility []struct {
			DisableCooling bool                      `json:"disable-cooling"`
			CountTokens    *config.CountTokensConfig `json:"count-tokens"`
			FakeNonStream  bool                      `json:"fake-non-stream"`
		} `json:"openai-compatibility"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.OpenAICompatibility) != 1 {
		t.Fatalf("openai-compatibility length = %d, want 1", len(body.OpenAICompatibility))
	}
	got := body.OpenAICompatibility[0]
	if !got.DisableCooling {
		t.Fatal("expected disable-cooling to be returned")
	}
	if got.CountTokens == nil || got.CountTokens.Mode != config.CountTokensModeDisabled {
		t.Fatalf("count-tokens = %#v, want disabled", got.CountTokens)
	}
	if !got.FakeNonStream {
		t.Fatal("expected fake-non-stream to be returned")
	}
}

func TestPatchOpenAICompatUpdatesProviderOptions(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("openai-compatibility: []\n"), 0o600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}
	h := &Handler{
		cfg: &config.Config{
			OpenAICompatibility: []config.OpenAICompatibility{{
				Name:    "compat",
				BaseURL: "https://example.com/v1",
			}},
		},
		configFilePath: configPath,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := `{"index":0,"value":{"disable-cooling":true,"count-tokens":{"mode":"disabled"},"fake-non-stream":true}}`
	c.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/openai-compatibility", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.PatchOpenAICompat(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	got := h.cfg.OpenAICompatibility[0]
	if !got.DisableCooling {
		t.Fatal("expected disable-cooling to be updated")
	}
	if got.CountTokens == nil || got.CountTokens.Mode != config.CountTokensModeDisabled {
		t.Fatalf("count-tokens = %#v, want disabled", got.CountTokens)
	}
	if !got.FakeNonStream {
		t.Fatal("expected fake-non-stream to be updated")
	}
}
