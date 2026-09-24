package config

import (
	"testing"
	"time"
)

func TestLoadConfigValid(t *testing.T) {
	setValidEnv(t)

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Listen != ":8080" {
		t.Fatalf("Listen = %q, want :8080", cfg.Listen)
	}
	if cfg.YKT.BaseURL != "https://example.com" {
		t.Fatalf("BaseURL = %q, want normalized URL", cfg.YKT.BaseURL)
	}
	if cfg.YKT.RefreshMargin != 5*time.Minute {
		t.Fatalf("RefreshMargin = %v, want 5m", cfg.YKT.RefreshMargin)
	}
	if cfg.YKT.Timeout != 20*time.Second {
		t.Fatalf("Timeout = %v, want 20s", cfg.YKT.Timeout)
	}
}

func TestLoadConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid timeout", key: "YKT_TIMEOUT", value: "0"},
		{name: "timeout too large", key: "YKT_TIMEOUT", value: "301"},
		{name: "non-numeric timeout", key: "YKT_TIMEOUT", value: "abc"},
		{name: "negative refresh margin", key: "YKT_REFRESH_MARGIN", value: "-1"},
		{name: "short gateway key", key: "API_PROXY_KEY", value: "short"},
		{name: "invalid listen address", key: "API_PROXY_LISTEN", value: "8080"},
		{name: "invalid base URL", key: "YKT_BASE_URL", value: "not-a-url"},
		{name: "base URL with query", key: "YKT_BASE_URL", value: "https://example.com?tenant=1"},
		{name: "base URL with userinfo", key: "YKT_BASE_URL", value: "https://user:pass@example.com"},
		{name: "max request body too small", key: "YKT_MAX_REQUEST_BYTES", value: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setValidEnv(t)
			t.Setenv(tt.key, tt.value)

			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig unexpectedly succeeded")
			}
		})
	}
}

func setValidEnv(t *testing.T) {
	t.Helper()

	t.Setenv("API_PROXY_LISTEN", ":8080")
	t.Setenv("API_PROXY_KEY", "")
	t.Setenv("YKT_ACCOUNT", "")
	t.Setenv("YKT_PASSWORD", "")
	t.Setenv("YKT_API_KEY", "test_key")
	t.Setenv("YKT_BASE_URL", "https://example.com/")
	t.Setenv("YKT_TOKEN_FILE", "token.json")
	t.Setenv("YKT_REFRESH_MARGIN", "300")
	t.Setenv("YKT_TIMEOUT", "20")
	t.Setenv("YKT_MAX_REQUEST_BYTES", "16777216")
}

func TestLoadConfigAllowsMissingUpstreamURL(t *testing.T) {
	setValidEnv(t)
	t.Setenv("YKT_BASE_URL", "")
	cfg, err := LoadConfig()
	if err != nil || cfg.YKT.BaseURL != "" {
		t.Fatalf("config=%#v error=%v", cfg, err)
	}
}
