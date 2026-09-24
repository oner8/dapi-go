package ykt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionPersistenceFailureKeepsMemory(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "missing", "token.json")
	session := NewSessionManager(tokenFile, 300)
	token := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())

	if err := session.SaveToken(token); err == nil {
		t.Fatal("SaveToken unexpectedly succeeded for a missing directory")
	}

	cache, ok := session.Snapshot()
	if !ok {
		t.Fatal("token was not retained in memory")
	}
	if cache.Token != token {
		t.Fatal("in-memory token does not match saved token")
	}
}

func TestSessionSaveTokenOverwritesAtomically(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token.json")
	session := NewSessionManager(tokenFile, 300)
	first := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())
	second := makeOperatorToken(t, time.Now().Add(2*time.Hour).Unix())

	if err := session.SaveToken(first); err != nil {
		t.Fatalf("save first token: %v", err)
	}
	if err := session.SaveToken(second); err != nil {
		t.Fatalf("overwrite token: %v", err)
	}

	data, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("read token file: %v", err)
	}
	var cache TokenCache
	if err := json.Unmarshal(data, &cache); err != nil {
		t.Fatalf("decode token file: %v", err)
	}
	if cache.Token != second {
		t.Fatal("token file was not replaced with the latest token")
	}

	leftovers, err := filepath.Glob(filepath.Join(dir, ".token.json.tmp-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary token files were not cleaned up: %v", leftovers)
	}
}

func TestHealthWithoutTokenReturnsNullExpiry(t *testing.T) {
	provider := newProxyTestProvider(t, "http://127.0.0.1:1", "", "")

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer test_key")
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if hasToken, _ := body["has_token"].(bool); hasToken {
		t.Fatal("has_token = true without a cached token")
	}
	if body["expires_in"] != nil {
		t.Fatalf("expires_in = %#v, want null", body["expires_in"])
	}
}

func TestHealthWithExpiredTokenReportsCachedToken(t *testing.T) {
	provider := newProxyTestProvider(t, "http://127.0.0.1:1", "", "")
	if err := provider.session.SaveToken(makeOperatorToken(t, time.Now().Add(-time.Minute).Unix())); err != nil {
		t.Fatalf("save expired token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer test_key")
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if hasToken, _ := body["has_token"].(bool); !hasToken {
		t.Fatal("has_token = false for an expired but cached token")
	}
	if expiresIn, ok := body["expires_in"].(float64); !ok || expiresIn != 0 {
		t.Fatalf("expires_in = %#v, want 0", body["expires_in"])
	}
}

func TestSessionLoadDerivesExpiryFromToken(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token.json")
	expiresAt := time.Now().Add(time.Hour).Unix()
	token := makeOperatorToken(t, expiresAt)
	data, err := json.Marshal(TokenCache{Token: token, ExpiresAt: expiresAt + 100000})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	session := NewSessionManager(file, 300)
	cache, ok := session.Snapshot()
	if !ok || cache.ExpiresAt != expiresAt {
		t.Fatalf("cache=%#v exists=%v", cache, ok)
	}
}
