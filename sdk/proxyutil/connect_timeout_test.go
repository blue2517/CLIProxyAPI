package proxyutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type recordingDeadlineConn struct {
	net.Conn
	deadlines []time.Time
}

func (c *recordingDeadlineConn) SetDeadline(deadline time.Time) error {
	c.deadlines = append(c.deadlines, deadline)
	return nil
}

func TestProxyConnectTimeoutHooksPreserveConnectionAndClearDeadline(t *testing.T) {
	base := &recordingDeadlineConn{}
	headerCalled, responseCalled := false, false
	transport := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return base, nil },
		GetProxyConnectHeader: func(context.Context, *url.URL, string) (http.Header, error) {
			headerCalled = true
			return http.Header{"X-Test": []string{"preserved"}}, nil
		},
		OnProxyConnectResponse: func(context.Context, *url.URL, *http.Request, *http.Response) error {
			responseCalled = true
			if len(base.deadlines) != 2 || !base.deadlines[1].IsZero() {
				t.Fatal("response callback must see cleared deadline")
			}
			return nil
		},
	}
	configureProxyConnectTimeout(transport, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, errDial := transport.DialContext(ctx, "tcp", "proxy.example:8080")
	if errDial != nil || conn != base {
		t.Fatalf("dial must preserve connection: %v", errDial)
	}
	if len(base.deadlines) != 0 {
		t.Fatal("ordinary proxy traffic must not have a CONNECT deadline")
	}
	header, errHeader := transport.GetProxyConnectHeader(ctx, nil, "upstream.example:443")
	if errHeader != nil || !headerCalled || header.Get("X-Test") != "preserved" {
		t.Fatalf("header callback: %v", errHeader)
	}
	if len(base.deadlines) != 1 || base.deadlines[0].IsZero() {
		t.Fatal("CONNECT must set a deadline")
	}
	if errResponse := transport.OnProxyConnectResponse(ctx, nil, nil, nil); errResponse != nil || !responseCalled {
		t.Fatalf("response callback: %v", errResponse)
	}
	if errResponse := transport.OnProxyConnectResponse(ctx, nil, nil, nil); errResponse != nil {
		t.Fatal(errResponse)
	}
	if len(base.deadlines) != 2 {
		t.Fatal("established tunnel must not set another deadline")
	}
}

func trustTLSProxy(t *testing.T, transport *http.Transport, server *httptest.Server, timeout time.Duration) {
	t.Helper()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	proxyURL, errParse := url.Parse(server.URL)
	if errParse != nil {
		t.Fatal(errParse)
	}
	// Reinstall hooks after replacing the dialer with one using the test CA.
	transport.DialTLSContext = buildHTTPSProxyDialTLSContext(proxyURL, &tls.Config{RootCAs: roots}, timeout, (&net.Dialer{Timeout: timeout}).DialContext)
	configureProxyConnectTimeout(transport, timeout)
}

func TestBuildHTTPTransportWithTimeoutBoundsCONNECT(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodConnect {
					t.Errorf("method = %s, want CONNECT", r.Method)
				}
				<-release
			}))
			if scheme == "https" {
				server.StartTLS()
			} else {
				server.Start()
			}
			t.Cleanup(func() { close(release); server.Close() })
			const timeout = 100 * time.Millisecond
			transport, _, errBuild := BuildHTTPTransportWithTimeout(server.URL, timeout)
			if errBuild != nil {
				t.Fatal(errBuild)
			}
			t.Cleanup(transport.CloseIdleConnections)
			if scheme == "https" {
				trustTLSProxy(t, transport, server, timeout)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, "https://upstream.invalid/", nil)
			if errRequest != nil {
				t.Fatal(errRequest)
			}
			response, errGet := (&http.Client{Transport: transport}).Do(req)
			if response != nil {
				_ = response.Body.Close()
			}
			var timeoutError net.Error
			if !errors.As(errGet, &timeoutError) || !timeoutError.Timeout() {
				t.Fatalf("request error = %v, want CONNECT timeout", errGet)
			}
			if ctx.Err() != nil {
				t.Fatalf("CONNECT used request deadline instead: %v", ctx.Err())
			}
		})
	}
}

func TestHTTPSProxyWithConnectTimeoutPreservesTLSStateAndTunnel(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "tunneled") }))
	defer upstream.Close()
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			_, _ = io.WriteString(w, "proxied")
			return
		}
		target, errDial := net.Dial("tcp", r.Host)
		if errDial != nil {
			t.Error(errDial)
			http.Error(w, "dial", http.StatusBadGateway)
			return
		}
		defer target.Close()
		client, buffered, errHijack := w.(http.Hijacker).Hijack()
		if errHijack != nil {
			t.Error(errHijack)
			return
		}
		defer client.Close()
		if _, errWrite := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); errWrite != nil {
			t.Error(errWrite)
			return
		}
		if errFlush := buffered.Flush(); errFlush != nil {
			t.Error(errFlush)
			return
		}
		copied := make(chan struct{})
		go func() { _, _ = io.Copy(target, buffered); _ = target.Close(); close(copied) }()
		_, _ = io.Copy(client, target)
		_ = client.Close()
		<-copied
	}))
	defer proxy.Close()
	transport, _, errBuild := BuildHTTPTransportWithTimeout(proxy.URL, time.Second)
	if errBuild != nil {
		t.Fatal(errBuild)
	}
	defer transport.CloseIdleConnections()
	trustTLSProxy(t, transport, proxy, time.Second)
	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	for _, target := range []struct{ url, body string }{{"http://upstream.invalid/", "proxied"}, {upstream.URL, "tunneled"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, target.url, nil)
		if errRequest != nil {
			cancel()
			t.Fatal(errRequest)
		}
		response, errGet := (&http.Client{Transport: transport}).Do(req)
		if errGet != nil {
			cancel()
			t.Fatal(errGet)
		}
		body, errRead := io.ReadAll(response.Body)
		_ = response.Body.Close()
		cancel()
		if errRead != nil || string(body) != target.body {
			t.Fatalf("response = %q, error = %v", body, errRead)
		}
		if response.TLS == nil || !response.TLS.HandshakeComplete {
			t.Fatal("TLS connection state must be preserved")
		}
	}
}
