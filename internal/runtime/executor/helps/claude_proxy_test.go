package helps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func TestClaudeHTTPClientInvalidProxyNeverFallsBack(t *testing.T) {
	var upstreamCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	for _, scope := range []string{"global", "credential", "request"} {
		for _, invalidProxy := range []string{"missing-scheme", "ftp://user:secret@proxy.invalid:80", "http://user:secret@%"} {
			t.Run(scope+"/"+invalidProxy, func(t *testing.T) {
				ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", roundTripperFunc(func(*http.Request) (*http.Response, error) {
					upstreamCalls.Add(1)
					return nil, errors.New("unexpected context transport")
				}))
				cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: "direct"}}
				auth := &cliproxyauth.Auth{Provider: "claude"}
				switch scope {
				case "global":
					cfg.ProxyURL = invalidProxy
				case "credential":
					auth.ProxyURL = invalidProxy
				case "request":
					auth.ProxyURL = "direct"
					ctx = cliproxyexecutor.WithRequestProxyURL(ctx, invalidProxy)
				}
				client := NewClaudeHTTPClient(ctx, cfg, auth, 0)
				defer client.CloseIdleConnections()
				for _, target := range []string{server.URL, "https://api.anthropic.com/v1/messages", "https://api.anthropic.com/v1/messages/count_tokens"} {
					resp, errRequest := client.Get(target)
					if resp != nil {
						if errClose := resp.Body.Close(); errClose != nil {
							t.Error(errClose)
						}
					}
					if errRequest == nil || !strings.Contains(errRequest.Error(), "invalid proxy configuration") {
						t.Fatalf("expected proxy configuration error, got %v", errRequest)
					}
					if strings.Contains(errRequest.Error(), "secret") {
						t.Fatal("proxy credentials leaked in error")
					}
				}
			})
		}
	}
	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("invalid proxy caused %d upstream/context requests", got)
	}
}

func TestClaudeCodeRoundTripperRejectsInvalidProxy(t *testing.T) {
	for _, proxyURL := range []string{"missing-scheme", "ftp://proxy.invalid"} {
		rt := newClaudeCodeRoundTripper(proxyURL)
		transport, ok := rt.(*http.Transport)
		if !ok {
			t.Fatalf("transport = %T, want Claude transport", rt)
		}
		defer transport.CloseIdleConnections()
		conn, errDial := transport.DialTLSContext(context.Background(), "tcp", "127.0.0.1:1")
		if conn != nil || errDial == nil || !strings.Contains(errDial.Error(), "invalid proxy configuration") {
			t.Fatalf("connection=%v error=%v, want proxy configuration error", conn, errDial)
		}
	}
}

func TestClaudeHTTPClientProxyPriority(t *testing.T) {
	for _, tc := range []struct {
		name, global, credential, request string
	}{
		{name: "absent"},
		{name: "explicit direct", global: "direct"},
		{name: "credential overrides invalid global", global: "bad", credential: "direct"},
		{name: "request overrides invalid credential", global: "bad", credential: "bad", request: "direct"},
		{name: "credential proxy overrides invalid global", global: "bad", credential: "http://proxy.invalid:8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := cliproxyexecutor.WithRequestProxyURL(context.Background(), tc.request)
			client := NewClaudeHTTPClient(ctx, &config.Config{SDKConfig: config.SDKConfig{ProxyURL: tc.global}}, &cliproxyauth.Auth{ProxyURL: tc.credential}, 0)
			defer client.CloseIdleConnections()
			rt, ok := client.Transport.(*fallbackRoundTripper)
			if !ok {
				t.Fatalf("transport = %T, want normal routing", client.Transport)
			}
			if _, ok := rt.anthropic.(*http.Transport); !ok {
				t.Fatalf("Anthropic transport = %T, want valid transport", rt.anthropic)
			}
		})
	}
}
