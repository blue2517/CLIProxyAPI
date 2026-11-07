package claude

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"golang.org/x/net/proxy"
)

func TestClaudeOAuthInvalidProxyNeverDials(t *testing.T) {
	for _, proxyURL := range []string{"missing-scheme", "ftp://user:secret@proxy.invalid:80", "http://user:secret@%"} {
		for _, override := range []bool{false, true} {
			cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: proxyURL}}
			proxyOverride := ""
			if override {
				cfg.ProxyURL = "http://fallback.invalid:8080"
				proxyOverride = proxyURL
			}
			auth := NewClaudeAuthWithProxyURL(cfg, proxyOverride)
			transport := auth.httpClient.Transport.(*utlsRoundTripper)
			dials := 0
			failDial := func(context.Context, string, string) (net.Conn, error) {
				dials++
				return nil, errors.New("unexpected network dial")
			}
			transport.transport.DialContext = failDial
			transport.dialer = claudeProxyTestDialer{dial: failDial}
			for _, target := range []string{"http://upstream.invalid/token", TokenURL, ProfileURL, RolesURL} {
				req, errRequest := http.NewRequest(http.MethodGet, target, nil)
				if errRequest != nil {
					t.Fatal(errRequest)
				}
				resp, errRequest := transport.RoundTrip(req)
				if resp != nil || errRequest == nil || !strings.Contains(errRequest.Error(), "invalid proxy configuration") {
					t.Fatalf("override=%v target=%s: response=%v error=%v", override, target, resp, errRequest)
				}
				if strings.Contains(errRequest.Error(), "secret") {
					t.Fatal("proxy credentials leaked in error")
				}
			}
			if _, errDial := transport.dialTLSContext(context.Background(), "tcp", "upstream.invalid:443"); errDial == nil {
				t.Fatal("TLS dial accepted invalid proxy configuration")
			}
			if dials != 0 {
				t.Fatalf("invalid proxy caused %d network dials", dials)
			}
			transport.CloseIdleConnections()
		}
	}
}

type claudeProxyTestDialer struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func (d claudeProxyTestDialer) Dial(network, addr string) (net.Conn, error) {
	return d.dial(context.Background(), network, addr)
}

func (d claudeProxyTestDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	return d.dial(ctx, network, addr)
}

func TestNewClaudeAuthWithProxyURL_OverrideDirectTakesPrecedence(t *testing.T) {
	cfg := &config.Config{SDKConfig: config.SDKConfig{ProxyURL: "socks5://proxy.example.com:1080"}}
	auth := NewClaudeAuthWithProxyURL(cfg, "direct")

	transport, ok := auth.httpClient.Transport.(*utlsRoundTripper)
	if !ok || transport == nil {
		t.Fatalf("expected utlsRoundTripper, got %T", auth.httpClient.Transport)
	}
	if transport.dialer != proxy.Direct {
		t.Fatalf("expected proxy.Direct, got %T", transport.dialer)
	}
}

func TestNewClaudeAuthWithProxyURL_OverrideProxyAppliedWithoutConfig(t *testing.T) {
	auth := NewClaudeAuthWithProxyURL(nil, "socks5://proxy.example.com:1080")

	transport, ok := auth.httpClient.Transport.(*utlsRoundTripper)
	if !ok || transport == nil {
		t.Fatalf("expected utlsRoundTripper, got %T", auth.httpClient.Transport)
	}
	if transport.dialer == proxy.Direct {
		t.Fatalf("expected proxy dialer, got %T", transport.dialer)
	}
}
