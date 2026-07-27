package executor

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestAntigravityProxyTimeoutScopesTransportCache(t *testing.T) {
	auth := &cliproxyauth.Auth{ID: "proxy-timeout-test"}
	firstConfig := &config.Config{}
	firstConfig.ProxyConnectTimeoutSeconds = 17
	secondConfig := &config.Config{}
	secondConfig.ProxyConnectTimeoutSeconds = 23
	first := antigravityProxiedHTTP11Transport(auth, "http://proxy.example:8080", firstConfig)
	again := antigravityProxiedHTTP11Transport(auth, "http://proxy.example:8080", firstConfig)
	second := antigravityProxiedHTTP11Transport(auth, "http://proxy.example:8080", secondConfig)
	if first == nil || second == nil || first != again || first == second {
		t.Fatal("Antigravity proxy transport cache must include connection timeout")
	}
	if first.TLSHandshakeTimeout != 17*time.Second || second.TLSHandshakeTimeout != 23*time.Second {
		t.Fatal("Antigravity proxy transport lost its configured handshake timeout")
	}
}
