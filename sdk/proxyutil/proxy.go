package proxyutil

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

// Mode describes how a proxy setting should be interpreted.
type Mode int

const (
	// ModeInherit means no explicit proxy behavior was configured.
	ModeInherit Mode = iota
	// ModeDirect means outbound requests must bypass proxies explicitly.
	ModeDirect
	// ModeProxy means a concrete proxy URL was configured.
	ModeProxy
	// ModeInvalid means the proxy setting is present but malformed or unsupported.
	ModeInvalid
)

// Setting is the normalized interpretation of a proxy configuration value.
type Setting struct {
	Raw  string
	Mode Mode
	URL  *url.URL
}

// Parse normalizes a proxy configuration value into inherit, direct, or proxy modes.
func Parse(raw string) (Setting, error) {
	trimmed := strings.TrimSpace(raw)
	setting := Setting{Raw: trimmed}

	if trimmed == "" {
		setting.Mode = ModeInherit
		return setting, nil
	}

	if strings.EqualFold(trimmed, "direct") || strings.EqualFold(trimmed, "none") {
		setting.Mode = ModeDirect
		return setting, nil
	}

	parsedURL, errParse := url.Parse(trimmed)
	if errParse != nil {
		setting.Mode = ModeInvalid
		return setting, fmt.Errorf("parse proxy URL failed")
	}
	if parsedURL.Scheme == "" || parsedURL.Host == "" {
		setting.Mode = ModeInvalid
		return setting, fmt.Errorf("proxy URL missing scheme/host")
	}

	switch parsedURL.Scheme {
	case "socks5", "socks5h", "http", "https":
		setting.Mode = ModeProxy
		setting.URL = parsedURL
		return setting, nil
	default:
		setting.Mode = ModeInvalid
		return setting, fmt.Errorf("unsupported proxy scheme: %s", parsedURL.Scheme)
	}
}

func cloneDefaultTransport() *http.Transport {
	if transport, ok := http.DefaultTransport.(*http.Transport); ok && transport != nil {
		return transport.Clone()
	}
	return &http.Transport{}
}

// NewDirectTransport returns a transport that bypasses environment proxies.
func NewDirectTransport() *http.Transport {
	clone := cloneDefaultTransport()
	clone.Proxy = nil
	return clone
}

// BuildHTTPTransport constructs an HTTP transport for the provided proxy setting.
func BuildHTTPTransport(raw string) (*http.Transport, Mode, error) {
	return BuildHTTPTransportWithTimeout(raw, 0)
}

// BuildHTTPTransportWithTimeout constructs an HTTP transport that bounds the
// connection-establishment phase by the provided timeout. A non-positive
// timeout preserves the default transport behavior. For HTTP/HTTPS proxies the
// cloned default transport already bounds dialing and TLS handshakes; for SOCKS5
// proxies the custom DialContext is bounded explicitly.
func BuildHTTPTransportWithTimeout(raw string, timeout time.Duration) (*http.Transport, Mode, error) {
	setting, errParse := Parse(raw)
	if errParse != nil {
		return nil, setting.Mode, errParse
	}

	switch setting.Mode {
	case ModeInherit:
		return nil, setting.Mode, nil
	case ModeDirect:
		return NewDirectTransport(), setting.Mode, nil
	case ModeProxy:
		if setting.URL.Scheme == "socks5" || setting.URL.Scheme == "socks5h" {
			var proxyAuth *proxy.Auth
			if setting.URL.User != nil {
				username := setting.URL.User.Username()
				password, _ := setting.URL.User.Password()
				proxyAuth = &proxy.Auth{User: username, Password: password}
			}
			dialer, errSOCKS5 := proxy.SOCKS5("tcp", setting.URL.Host, proxyAuth, proxy.Direct)
			if errSOCKS5 != nil {
				return nil, setting.Mode, fmt.Errorf("create SOCKS5 dialer failed: %w", errSOCKS5)
			}
			transport := cloneDefaultTransport()
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
				if timeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, timeout)
					defer cancel()
				}
				if ctxDialer, ok := dialer.(proxy.ContextDialer); ok {
					return ctxDialer.DialContext(ctx, network, addr)
				}
				return dialer.Dial(network, addr)
			}
			return transport, setting.Mode, nil
		}
		transport := cloneDefaultTransport()
		transport.Proxy = http.ProxyURL(setting.URL)
		if timeout > 0 {
			transport.TLSHandshakeTimeout = timeout
			baseDialer := &net.Dialer{Timeout: timeout}
			transport.DialContext = baseDialer.DialContext
		}
		return transport, setting.Mode, nil
	default:
		return nil, setting.Mode, nil
	}
}

// BuildDialer constructs a proxy dialer for settings that operate at the connection layer.
func BuildDialer(raw string) (proxy.Dialer, Mode, error) {
	return BuildDialerWithTimeout(raw, 0)
}

// BuildDialerWithTimeout constructs a proxy dialer that bounds the
// connection-establishment phase (dial + TLS handshake + HTTP CONNECT) by the
// provided timeout. A non-positive timeout disables the bound. The deadline is
// always cleared before the connection is returned, so it never applies to
// subsequent reads/writes on the established connection.
func BuildDialerWithTimeout(raw string, timeout time.Duration) (proxy.Dialer, Mode, error) {
	setting, errParse := Parse(raw)
	if errParse != nil {
		return nil, setting.Mode, errParse
	}

	switch setting.Mode {
	case ModeInherit:
		return nil, setting.Mode, nil
	case ModeDirect:
		return proxy.Direct, setting.Mode, nil
	case ModeProxy:
		if setting.URL.Scheme == "http" || setting.URL.Scheme == "https" {
			return &httpConnectDialer{proxyURL: setting.URL, dialer: proxy.Direct, timeout: timeout}, setting.Mode, nil
		}
		dialer, errDialer := proxy.FromURL(setting.URL, proxy.Direct)
		if errDialer != nil {
			return nil, setting.Mode, fmt.Errorf("create proxy dialer failed: %w", errDialer)
		}
		if timeout > 0 {
			return &timeoutDialer{dialer: dialer, timeout: timeout}, setting.Mode, nil
		}
		return dialer, setting.Mode, nil
	default:
		return nil, setting.Mode, nil
	}
}

// timeoutDialer bounds the dial of an underlying ContextDialer (e.g. the SOCKS5
// dialer from x/net/proxy) by a timeout that only covers establishing the
// connection. The context is cancelled once Dial returns; x/net's SOCKS5 dialer
// does not retain the context past connection setup, so the established
// connection is unaffected.
type timeoutDialer struct {
	dialer  proxy.Dialer
	timeout time.Duration
}

func (d *timeoutDialer) Dial(network, addr string) (net.Conn, error) {
	if ctxDialer, ok := d.dialer.(proxy.ContextDialer); ok {
		ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
		defer cancel()
		return ctxDialer.DialContext(ctx, network, addr)
	}
	return d.dialer.Dial(network, addr)
}

type httpConnectDialer struct {
	proxyURL *url.URL
	dialer   proxy.Dialer
	timeout  time.Duration
}

func (d *httpConnectDialer) Dial(network, addr string) (net.Conn, error) {
	var deadline time.Time
	if d.timeout > 0 {
		deadline = time.Now().Add(d.timeout)
	}

	proxyConn, errDial := d.dialProxy(network, deadline)
	if errDial != nil {
		return nil, fmt.Errorf("dial HTTP proxy failed: %w", errDial)
	}

	conn := proxyConn
	// Bound the CONNECT handshake (and TLS handshake for https proxies) by the
	// connection-establishment deadline. It is cleared before returning so reads
	// and writes on the tunneled connection are never time-bounded.
	if !deadline.IsZero() {
		if errDeadline := conn.SetDeadline(deadline); errDeadline != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("set proxy connect deadline failed: %w", errDeadline)
		}
	}
	if d.proxyURL.Scheme == "https" {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: d.proxyURL.Hostname()})
		if errHandshake := tlsConn.Handshake(); errHandshake != nil {
			if errClose := conn.Close(); errClose != nil {
				return nil, fmt.Errorf("HTTPS proxy TLS handshake failed: %w; close failed: %v", errHandshake, errClose)
			}
			return nil, fmt.Errorf("HTTPS proxy TLS handshake failed: %w", errHandshake)
		}
		conn = tlsConn
	}

	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Host: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if d.proxyURL.User != nil {
		req.Header.Set("Proxy-Authorization", proxyAuthorization(d.proxyURL.User))
	}
	if errWrite := req.Write(conn); errWrite != nil {
		if errClose := conn.Close(); errClose != nil {
			return nil, fmt.Errorf("write CONNECT request failed: %w; close failed: %v", errWrite, errClose)
		}
		return nil, fmt.Errorf("write CONNECT request failed: %w", errWrite)
	}

	reader := bufio.NewReader(conn)
	resp, errRead := http.ReadResponse(reader, req)
	if errRead != nil {
		if errClose := conn.Close(); errClose != nil {
			return nil, fmt.Errorf("read CONNECT response failed: %w; close failed: %v", errRead, errClose)
		}
		return nil, fmt.Errorf("read CONNECT response failed: %w", errRead)
	}
	if resp.StatusCode != http.StatusOK {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if errClose := conn.Close(); errClose != nil {
			return nil, fmt.Errorf("proxy CONNECT returned status %s; close failed: %v", resp.Status, errClose)
		}
		return nil, fmt.Errorf("proxy CONNECT returned status %s", resp.Status)
	}

	// Connection established: clear the deadline so subsequent reads/writes on the
	// tunneled connection are never time-bounded.
	if !deadline.IsZero() {
		if errClear := conn.SetDeadline(time.Time{}); errClear != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("clear proxy connect deadline failed: %w", errClear)
		}
	}

	if reader.Buffered() > 0 {
		return &bufferedConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

// dialProxy dials the proxy server, bounding the dial by deadline when set.
func (d *httpConnectDialer) dialProxy(network string, deadline time.Time) (net.Conn, error) {
	addr := proxyDialAddr(d.proxyURL)
	if deadline.IsZero() {
		return d.dialer.Dial(network, addr)
	}
	if ctxDialer, ok := d.dialer.(proxy.ContextDialer); ok {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		return ctxDialer.DialContext(ctx, network, addr)
	}
	// proxy.Direct implements ContextDialer, so this fallback is only reached for
	// custom dialers without context support.
	return d.dialer.Dial(network, addr)
}

func proxyDialAddr(proxyURL *url.URL) string {
	port := proxyURL.Port()
	if port == "" {
		port = "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(proxyURL.Hostname(), port)
}

func proxyAuthorization(user *url.Userinfo) string {
	username := user.Username()
	password, _ := user.Password()
	encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	return "Basic " + encoded
}

// Redact returns a log-safe proxy URL with credentials and path-like data removed.
func Redact(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parsedURL, errParse := url.Parse(trimmed)
	if errParse != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return "<invalid proxy URL>"
	}

	redacted := &url.URL{
		Scheme: parsedURL.Scheme,
		Host:   parsedURL.Host,
	}
	if parsedURL.User != nil {
		redacted.User = url.User("redacted")
	}
	return redacted.String()
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if c.reader.Buffered() > 0 {
		return c.reader.Read(p)
	}
	return c.Conn.Read(p)
}
