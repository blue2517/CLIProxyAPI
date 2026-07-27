package config

import (
	"testing"
	"time"
)

func TestSDKConfigProxyConnectTimeout(t *testing.T) {
	t.Parallel()

	if got := (*SDKConfig)(nil).ProxyConnectTimeout(); got != DefaultProxyConnectTimeout {
		t.Fatalf("nil config timeout = %v, want %v", got, DefaultProxyConnectTimeout)
	}
	if got := (&SDKConfig{}).ProxyConnectTimeout(); got != DefaultProxyConnectTimeout {
		t.Fatalf("default timeout = %v, want %v", got, DefaultProxyConnectTimeout)
	}
	if got := (&SDKConfig{ProxyConnectTimeoutSeconds: 23}).ProxyConnectTimeout(); got != 23*time.Second {
		t.Fatalf("configured timeout = %v, want 23s", got)
	}
}
