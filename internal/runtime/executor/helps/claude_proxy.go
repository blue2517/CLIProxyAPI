package helps

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/httptransport"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/proxyutil"
)

type claudeProxyError struct {
	err error
}

func (rt claudeProxyError) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, rt.err
}

func (rt claudeProxyError) Dial(string, string) (net.Conn, error) {
	return nil, rt.err
}

func (rt claudeProxyError) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, rt.err
}

// NewClaudeHTTPClient rejects invalid proxy settings before selecting any
// transport, including context transports and custom upstream gateways.
// An absent proxy or an explicit direct setting retains its existing meaning.
func NewClaudeHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	if _, _, errBuild := proxyutil.BuildDialer(effectiveProxyURL(ctx, cfg, auth)); errBuild != nil {
		return httptransport.Decorate(ctx, &http.Client{
			Transport: claudeProxyError{err: fmt.Errorf("claude: invalid proxy configuration: %w", errBuild)},
			Timeout:   timeout,
		})
	}
	return NewUtlsHTTPClient(ctx, cfg, auth, timeout)
}
