package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxyConnectTimeoutV8MigrationAndPersistence(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        int
	}{
		{"legacy", "proxy-connect-timeout-seconds: 17\n", 17},
		{"v8", "config-version: 8\nrequests:\n  proxy-connect-timeout-seconds: 23\n", 23},
		{"v8 zero wins", "proxy-connect-timeout-seconds: 17\nrequests:\n  proxy-connect-timeout-seconds: 0\n", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			migrated, _, errNormalize := NormalizeConfigLayout([]byte(tc.input), true)
			if errNormalize != nil {
				t.Fatal(errNormalize)
			}
			if errValidate := ValidateV8Config(migrated); errValidate != nil {
				t.Fatal(errValidate)
			}
			cfg, errParse := ParseConfigBytes(migrated)
			if errParse != nil {
				t.Fatal(errParse)
			}
			if cfg.ProxyConnectTimeoutSeconds != tc.want {
				t.Fatalf("timeout = %d, want %d; yaml=%s", cfg.ProxyConnectTimeoutSeconds, tc.want, migrated)
			}
			if !strings.Contains(string(migrated), "proxy-connect-timeout-seconds:") {
				t.Fatalf("migration lost the timeout field: %s", migrated)
			}
			path := filepath.Join(t.TempDir(), "config.yaml")
			if errWrite := os.WriteFile(path, migrated, 0600); errWrite != nil {
				t.Fatal(errWrite)
			}
			if errSave := SaveConfigPreserveComments(path, cfg); errSave != nil {
				t.Fatal(errSave)
			}
			reloaded, errLoad := LoadConfig(path)
			if errLoad != nil {
				t.Fatal(errLoad)
			}
			if reloaded.ProxyConnectTimeoutSeconds != tc.want {
				t.Fatalf("saved timeout = %d, want %d", reloaded.ProxyConnectTimeoutSeconds, tc.want)
			}
		})
	}
}

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
