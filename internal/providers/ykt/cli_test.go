package ykt

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"dapi-go/internal/config"
)

func TestLoginAndSavePersistsToken(t *testing.T) {
	token := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())
	upstream := newLoginTestServer(t, token, nil)
	defer upstream.Close()

	tokenFile := filepath.Join(t.TempDir(), "token.json")
	cache, err := LoginAndSave(config.YKTConfig{
		Account:       "test_account",
		Password:      "test_password",
		BaseURL:       upstream.URL,
		TokenFile:     tokenFile,
		RefreshMargin: 300 * time.Second,
		Timeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatalf("LoginAndSave() error = %v", err)
	}
	if cache.Token != token {
		t.Fatal("returned cache does not contain the logged-in token")
	}

	session := NewSessionManager(tokenFile, 0)
	saved, ok := session.Snapshot()
	if !ok {
		t.Fatal("token was not persisted")
	}
	if saved.Token != token {
		t.Fatal("persisted token does not match")
	}
}

func TestLoginAndSaveRetriesOnce(t *testing.T) {
	token := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())
	var attempts atomic.Int32
	upstream := newLoginTestServer(t, token, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/login/findVerificationCode" {
			return false
		}
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary failure", http.StatusBadGateway)
			return true
		}
		return false
	})
	defer upstream.Close()

	_, err := LoginAndSave(config.YKTConfig{
		Account:       "test_account",
		Password:      "test_password",
		BaseURL:       upstream.URL,
		TokenFile:     filepath.Join(t.TempDir(), "token.json"),
		RefreshMargin: 300 * time.Second,
		Timeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatalf("LoginAndSave() error = %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("captcha attempts = %d, want 2", got)
	}
}

func TestDecodeTokenFileRejectsMissingToken(t *testing.T) {
	_, err := DecodeTokenFile(filepath.Join(t.TempDir(), "missing.json"))
	if err == nil {
		t.Fatal("DecodeTokenFile() unexpectedly succeeded")
	}
}

func newLoginTestServer(
	t *testing.T,
	token string,
	intercept func(http.ResponseWriter, *http.Request) bool,
) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if intercept != nil && intercept(w, r) {
			return
		}

		switch r.URL.Path {
		case "/api/login/findVerificationCode":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": "123456"})
		case "/api/login/operatorCheckLogin":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"operator_msg": "operator",
					"area": []map[string]interface{}{
						{"id": 147},
					},
				},
			})
		case "/api/login/operatorCheckIndex":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"userInfo": map[string]interface{}{"token": token},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestLoginRequiresUpstreamURL(t *testing.T) {
	_, err := LoginAndSave(config.YKTConfig{Account: "a", Password: "b", TokenFile: filepath.Join(t.TempDir(), "token.json")})
	if err != ErrMissingBaseURL {
		t.Fatalf("error = %v, want ErrMissingBaseURL", err)
	}
}

func TestLoginUsesBrowserHeaders(t *testing.T) {
	token := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())
	requests := make(map[string]http.Header)
	upstream := newLoginTestServer(t, token, func(w http.ResponseWriter, r *http.Request) bool {
		requests[r.URL.Path] = r.Header.Clone()
		return false
	})
	defer upstream.Close()

	_, err := LoginAndSave(config.YKTConfig{
		Account:       "test_account",
		Password:      "test_password",
		BaseURL:       upstream.URL,
		TokenFile:     filepath.Join(t.TempDir(), "token.json"),
		RefreshMargin: 300 * time.Second,
		Timeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/login/findVerificationCode",
		"/api/login/operatorCheckLogin",
		"/api/login/operatorCheckIndex",
	} {
		header := requests[path]
		if header == nil {
			t.Errorf("no request to %s", path)
			continue
		}
		if got := header.Get("User-Agent"); got != browserUserAgent {
			t.Errorf("%s User-Agent = %q", path, got)
		}
		if got := header.Get("Referer"); got != upstream.URL+"/login" {
			t.Errorf("%s Referer = %q", path, got)
		}
		if got := header.Get("Sec-Fetch-Site"); got != "same-origin" {
			t.Errorf("%s Sec-Fetch-Site = %q", path, got)
		}
		wantOrigin := ""
		if path == "/api/login/operatorCheckLogin" {
			wantOrigin = upstream.URL
		}
		if got := header.Get("Origin"); got != wantOrigin {
			t.Errorf("%s Origin = %q, want %q", path, got, wantOrigin)
		}
	}
}
