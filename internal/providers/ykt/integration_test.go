package ykt

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"dapi-go/internal/config"
)

// TestProviderIntegration tests the full provider flow with a mock upstream.
func TestProviderIntegration(t *testing.T) {
	token := makeOperatorToken(t, time.Now().Add(time.Hour).Unix())
	// Create a mock ykt upstream server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login/findVerificationCode" {
			// Return mock captcha
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": "123456",
			})
			return
		}
		if r.URL.Path == "/api/login/operatorCheckLogin" {
			// Return mock operator_msg and area
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"operator_msg": "test_operator",
					"area": []map[string]interface{}{
						{
							"id":   147,
							"name": "test_area",
						},
					},
				},
			})
			return
		}
		if r.URL.Path == "/api/login/operatorCheckIndex" {
			// Return mock token (simplified, not a real JWT)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"data": map[string]interface{}{
					"userInfo": map[string]interface{}{
						"token": token,
					},
				},
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer mockServer.Close()

	// Create provider
	cfg := config.YKTConfig{
		Account:       "test_account",
		Password:      "test_password",
		APIKey:        "test_key",
		BaseURL:       mockServer.URL,
		TokenFile:     filepath.Join(t.TempDir(), "test-token.json"),
		RefreshMargin: 300,
	}

	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	handler := provider.Handler()

	// Test 1: Token endpoint without auth -> 401
	t.Run("token endpoint without auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/token", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", w.Code)
		}
	})

	// Test 2: Token endpoint with valid auth -> 200
	t.Run("token endpoint with valid auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/token", nil)
		req.Header.Set("Authorization", "Bearer test_key")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", w.Code)
		}

		// Verify response contains token
		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Errorf("Failed to unmarshal response: %v", err)
		}
		if _, ok := resp["token"]; !ok {
			t.Errorf("Response missing token field")
		}
	})

	// Test 3: Health endpoint with valid auth -> 200
	t.Run("health endpoint with valid auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header.Set("Authorization", "Bearer test_key")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", w.Code)
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Errorf("Failed to unmarshal response: %v", err)
		}
		if status, ok := resp["status"].(string); !ok || status != "ok" {
			t.Errorf("Response missing or invalid status field")
		}
	})

	t.Run("legacy header does not authenticate", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set("HSYCF-API-KEY", "test_key")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for legacy header, got %d", w.Code)
		}
	})

	// Test 4: Invalid Bearer token -> 401
	t.Run("invalid bearer token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/token", nil)
		req.Header.Set("Authorization", "Bearer wrong_key")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", w.Code)
		}
	})

	// Test 5: Unmapped path -> 404
	t.Run("unmapped path", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/unknown", nil)
		req.Header.Set("Authorization", "Bearer test_key")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("Expected 404, got %d", w.Code)
		}
	})
}

// TestSessionCaching tests token caching logic.
func TestSessionCaching(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "test-session.json")
	sm := NewSessionManager(tokenFile, 300)

	// Test 1: No cached token
	_, valid, _ := sm.GetToken()
	if valid {
		t.Errorf("Expected no valid token initially")
	}

	// Test 2: After saving a token, it should be returned as-is.
	// ParseToken needs a "operator" + zlib-compressed JWT; build one with
	// exp far in the future.
	payload := map[string]int64{"exp": 9999999999, "iat": 1726132800}
	raw, _ := json.Marshal(payload)
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(raw)
	zw.Close()
	payloadB64 := base64.RawURLEncoding.EncodeToString(buf.Bytes())
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS512"}`))
	mockToken := "operator" + headerB64 + "." + payloadB64 + ".signature"

	if err := sm.SaveToken(mockToken); err != nil {
		t.Fatalf("SaveToken failed: %v", err)
	}

	token, valid, err := sm.GetToken()
	if err != nil {
		t.Errorf("GetToken failed: %v", err)
	}
	if !valid {
		t.Errorf("Expected valid token after save")
	}
	if token != mockToken {
		t.Errorf("Token mismatch: expected %s, got %s", mockToken, token)
	}
}
