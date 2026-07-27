package proxyutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// configureProxyConnectTimeout bounds only the CONNECT exchange. It preserves
// concrete connection types (notably *tls.Conn) and clears the deadline before
// upstream TLS or application traffic begins.
func configureProxyConnectTimeout(transport *http.Transport, timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	var connections sync.Map
	remember := func(ctx context.Context, conn net.Conn) {
		connections.Store(ctx, conn)
		context.AfterFunc(ctx, func() { connections.Delete(ctx) })
	}
	if dial := transport.DialContext; dial != nil {
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, errDial := dial(ctx, network, addr)
			if errDial == nil {
				remember(ctx, conn)
			}
			return conn, errDial
		}
	}
	if dialTLS := transport.DialTLSContext; dialTLS != nil {
		transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, errDial := dialTLS(ctx, network, addr)
			if errDial == nil {
				remember(ctx, conn)
			}
			return conn, errDial
		}
	}
	previousHeader := transport.GetProxyConnectHeader
	transport.GetProxyConnectHeader = func(ctx context.Context, proxyURL *url.URL, target string) (http.Header, error) {
		if value, ok := connections.Load(ctx); ok {
			if errDeadline := value.(net.Conn).SetDeadline(time.Now().Add(timeout)); errDeadline != nil {
				return nil, fmt.Errorf("set proxy CONNECT deadline: %w", errDeadline)
			}
		}
		if previousHeader != nil {
			return previousHeader(ctx, proxyURL, target)
		}
		return transport.ProxyConnectHeader, nil
	}
	previousResponse := transport.OnProxyConnectResponse
	transport.OnProxyConnectResponse = func(ctx context.Context, proxyURL *url.URL, request *http.Request, response *http.Response) error {
		if value, ok := connections.LoadAndDelete(ctx); ok {
			if errClear := value.(net.Conn).SetDeadline(time.Time{}); errClear != nil {
				return fmt.Errorf("clear proxy CONNECT deadline: %w", errClear)
			}
		}
		if previousResponse != nil {
			return previousResponse(ctx, proxyURL, request, response)
		}
		return nil
	}
}
