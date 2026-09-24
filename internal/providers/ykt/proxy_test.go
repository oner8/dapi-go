package ykt

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dapi-go/internal/config"
)

func TestProxyLoginFailureReturns503(t *testing.T) {
	provider := newProxyTestProvider(t, "http://127.0.0.1:1", "", "")

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer test_key")
	rec := httptest.NewRecorder()

	provider.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected JSON content type, got %q", got)
	}
}

func TestProxyRetryReplaysRequestBody(t *testing.T) {
	const requestBody = `{"name":"retry-test"}`
	newToken := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())

	var (
		mu         sync.Mutex
		seenBodies []string
		calls      int
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
					"userInfo": map[string]interface{}{"token": newToken},
				},
			})
		case "/api/echo":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read upstream body: %v", err)
			}
			mu.Lock()
			seenBodies = append(seenBodies, string(body))
			calls++
			call := calls
			mu.Unlock()

			if call == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"code":"2001","message":"token invalid"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	provider := newProxyTestProvider(t, upstream.URL, "test_account", "test_password")
	if err := provider.session.SaveToken(makeOperatorToken(t, time.Now().Add(time.Hour).Unix())); err != nil {
		t.Fatalf("save initial token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/echo", bytes.NewBufferString(requestBody))
	req.Header.Set("Authorization", "Bearer test_key")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	provider.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != requestBody {
		t.Fatalf("unexpected response body %q", rec.Body.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenBodies) != 2 {
		t.Fatalf("expected 2 upstream requests, got %d", len(seenBodies))
	}
	for i, body := range seenBodies {
		if body != requestBody {
			t.Fatalf("upstream request %d body = %q, want %q", i+1, body, requestBody)
		}
	}
}

func TestProxyRequestsAreConcurrent(t *testing.T) {
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var calls atomic.Int32

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch call := calls.Add(1); call {
		case 1:
			close(firstStarted)
			<-releaseFirst
		case 2:
			close(secondStarted)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	provider := newProxyTestProvider(t, upstream.URL, "test_account", "test_password")
	if err := provider.session.SaveToken(makeOperatorToken(t, time.Now().Add(time.Hour).Unix())); err != nil {
		t.Fatalf("save initial token: %v", err)
	}

	runRequest := func() <-chan int {
		done := make(chan int, 1)
		go func() {
			req := httptest.NewRequest(http.MethodGet, "/api/slow", nil)
			req.Header.Set("Authorization", "Bearer test_key")
			rec := httptest.NewRecorder()
			provider.Handler().ServeHTTP(rec, req)
			done <- rec.Code
		}()
		return done
	}

	firstDone := runRequest()
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first proxy request did not reach upstream")
	}

	secondDone := runRequest()
	secondReachedUpstream := false
	select {
	case <-secondStarted:
		secondReachedUpstream = true
	case <-time.After(500 * time.Millisecond):
	}

	close(releaseFirst)
	for name, done := range map[string]<-chan int{"first": firstDone, "second": secondDone} {
		select {
		case code := <-done:
			if code != http.StatusOK {
				t.Fatalf("%s proxy request returned %d", name, code)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("%s proxy request did not finish", name)
		}
	}

	if !secondReachedUpstream {
		t.Fatal("second proxy request was blocked behind the first request")
	}
}

func newProxyTestProvider(t *testing.T, baseURL, account, password string) *Provider {
	t.Helper()

	provider, err := NewProvider(config.YKTConfig{
		Account:       account,
		Password:      password,
		APIKey:        "test_key",
		BaseURL:       baseURL,
		TokenFile:     filepath.Join(t.TempDir(), "token.json"),
		RefreshMargin: 300 * time.Second,
		Timeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatalf("create provider: %v", err)
	}
	t.Cleanup(provider.Close)
	return provider
}

func makeOperatorToken(t *testing.T, expiresAt int64) string {
	t.Helper()

	payload, err := json.Marshal(map[string]int64{
		"exp": expiresAt,
		"iat": time.Now().Add(-time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("marshal token payload: %v", err)
	}

	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		t.Fatalf("compress token payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close token compressor: %v", err)
	}

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"zip":"DEF","alg":"HS512"}`))
	body := base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	return "operator" + header + "." + body + ".signature"
}

func TestProxyFiltersHeaders(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Connection", "X-Hop-Response")
		w.Header().Set("X-Hop-Response", "hidden")
		w.Header().Set("Set-Cookie", "session=hidden")
		w.Header().Set("Token", "upstream-secret")
		w.Header().Set("X-End-to-End", "kept")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	provider := newProxyTestProvider(t, upstream.URL, "", "")
	token := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())
	if err := provider.session.SaveToken(token); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer test_key")
	req.Header.Set("X-Api-Key", "private-gateway-key")
	req.Header.Set("HSYCF-API-KEY", "legacy-key")
	req.Header.Set("Token", "caller-token")
	req.Header.Set("Cookie", "private=cookie")
	req.Header.Set("Connection", "X-Hop-Request")
	req.Header.Set("X-Hop-Request", "hidden")
	req.Header.Set("X-Forwarded-Port", "private-port")
	req.Header.Set("X-End-to-End", "kept")
	req.Header.Set("Accept", "application/custom")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got.Get("Token") != token || got.Get("X-End-to-End") != "kept" || got.Get("Accept") != "application/custom" {
		t.Fatalf("incorrect upstream headers: %#v", got)
	}
	for _, name := range []string{"Authorization", "X-Api-Key", "HSYCF-API-KEY", "Cookie", "X-Hop-Request", "X-Forwarded-Port", "Content-Type"} {
		if got.Get(name) != "" {
			t.Errorf("upstream received %s", name)
		}
	}
	if rec.Header().Get("X-Hop-Response") != "" || rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("Token") != "" || rec.Header().Get("X-End-to-End") != "kept" {
		t.Fatalf("incorrect downstream headers: %#v", rec.Header())
	}
}

func TestProxyCrossOriginRedirectDoesNotForwardToken(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirected.Add(1)
	}))
	defer other.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/capture", http.StatusFound)
	}))
	defer upstream.Close()
	provider := newProxyTestProvider(t, upstream.URL, "", "")
	if err := provider.session.SaveToken(makeOperatorToken(t, time.Now().Add(time.Hour).Unix())); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/redirect", nil)
	req.Header.Set("Authorization", "Bearer test_key")
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || redirected.Load() != 0 {
		t.Fatalf("status=%d redirected=%d", rec.Code, redirected.Load())
	}
}

func TestProxyRejectsOversizedBody(t *testing.T) {
	provider := newProxyTestProvider(t, "http://127.0.0.1:1", "", "")
	provider.cfg.MaxRequestBytes = 3
	req := httptest.NewRequest(http.MethodPost, "/api/test", bytes.NewBufferString("four"))
	req.Header.Set("Authorization", "Bearer test_key")
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestProxyCancellationStopsUpstreamRequest(t *testing.T) {
	var called atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
	}))
	defer upstream.Close()
	provider := newProxyTestProvider(t, upstream.URL, "", "")
	if err := provider.session.SaveToken(makeOperatorToken(t, time.Now().Add(time.Hour).Unix())); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer test_key")
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway || called.Load() != 0 {
		t.Fatalf("status=%d upstream_calls=%d", rec.Code, called.Load())
	}
}

func TestProxyBoardRequestUsesBrowserHeaders(t *testing.T) {
	var got http.Header
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	provider := newProxyTestProvider(t, upstream.URL, "", "")
	token := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())
	if err := provider.session.SaveToken(token); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/basic/findDataAreaBoard", nil)
	req.Header.Set("Authorization", "Bearer test_key")
	req.Header.Set("Token", "caller-token")
	req.Header.Set("User-Agent", "curl/8")
	req.Header.Set("Referer", "https://caller.invalid/private")
	req.Header.Set("Origin", "https://caller.invalid")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	provider.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	want := map[string]string{
		"Token":              token,
		"User-Agent":         browserUserAgent,
		"Accept":             "*/*",
		"Accept-Language":    "zh-CN,zh;q=0.9",
		"Referer":            upstream.URL + "/transactionDetail",
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
		"Sec-Ch-Ua-Mobile":   "?0",
		"Sec-Ch-Ua-Platform": `"Windows"`,
		"Cache-Control":      "no-cache",
		"Pragma":             "no-cache",
	}
	for name, value := range want {
		if got.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, got.Get(name), value)
		}
	}
	for _, name := range []string{"Origin", "Authorization", "X-Api-Key"} {
		if got.Get(name) != "" {
			t.Errorf("upstream received %s", name)
		}
	}
}
