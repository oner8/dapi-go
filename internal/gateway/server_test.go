package gateway

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"dapi-go/internal/config"
	"dapi-go/internal/provider"
	"dapi-go/internal/providers/ykt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogRequestOmitsQueryString(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	req := httptest.NewRequest("GET", "/api/test?token=secret-value", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	logRequest(req)

	got := output.String()
	if !strings.Contains(got, "method=GET") {
		t.Fatalf("log output missing method: %q", got)
	}
	if strings.Contains(got, "secret-value") || strings.Contains(got, "token=") || strings.Contains(got, "/api/test") || strings.Contains(got, "127.0.0.1") {
		t.Fatalf("log output leaked query string: %q", got)
	}
}

func TestYKTRoutesAndAuth(t *testing.T) {
	p, err := ykt.NewProvider(config.YKTConfig{
		APIKey:    "provider-key",
		BaseURL:   "http://127.0.0.1:1",
		TokenFile: filepath.Join(t.TempDir(), "token.json"),
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	reg := provider.NewRegistry()
	reg.Register(p)
	srv := NewServer()
	srv.RegisterHandlers(reg, reg.Handlers(), "gateway-key-12345")

	check := func(path, bearer, gatewayKey string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if gatewayKey != "" {
			req.Header.Set("X-Api-Key", gatewayKey)
		}
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%s: got %d want %d", path, rec.Code, want)
		}
		return rec
	}
	check("/ykt/health", "", "gateway-key-12345", http.StatusUnauthorized)
	check("/ykt/health", "provider-key", "", http.StatusUnauthorized)
	check("/ykt/health", "provider-key", "gateway-key-12345", http.StatusOK)
	check("/hsyxf/health", "provider-key", "gateway-key-12345", http.StatusNotFound)
	root := check("/", "", "", http.StatusOK)
	if root.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("root content type = %q", root.Header().Get("Content-Type"))
	}
	var rootBody map[string]string
	if err := json.Unmarshal(root.Body.Bytes(), &rootBody); err != nil {
		t.Fatal(err)
	}
	if rootBody["service"] != "dapi-go" || rootBody["status"] != "ok" || rootBody["health"] != "/health" {
		t.Fatalf("unexpected root response: %#v", rootBody)
	}
	check("/unknown", "", "", http.StatusNotFound)
	probe := check("/health", "", "", http.StatusOK)
	var body map[string]interface{}
	if err := json.Unmarshal(probe.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Fatalf("public health leaked details: %#v", body)
	}
}
