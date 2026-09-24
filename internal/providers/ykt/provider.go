package ykt

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"dapi-go/internal/config"
)

// Provider is the ykt provider implementation.
type Provider struct {
	cfg         config.YKTConfig
	session     *SessionManager
	login       *LoginClient
	proxyClient *http.Client
	ref         *refresher
	stop        chan struct{}
	stopOnce    sync.Once
	mu          sync.Mutex
	loginMu     sync.Mutex
	relogins    int64 // statistics
	served      int64 // statistics
}

// NewProvider creates a new ykt provider.
func NewProvider(cfg config.YKTConfig) (*Provider, error) {
	if cfg.BaseURL == "" {
		return nil, ErrMissingBaseURL
	}
	if cfg.APIKey == "" {
		return nil, ErrMissingAPIKey
	}

	p := &Provider{
		cfg:         cfg,
		session:     NewSessionManager(cfg.TokenFile, int64(cfg.RefreshMargin.Seconds())),
		login:       NewLoginClient(cfg.BaseURL, cfg.Account, cfg.Password, cfg.Timeout),
		proxyClient: newProxyClient(cfg.Timeout),
		stop:        make(chan struct{}),
	}
	p.ref = newRefresher(p)
	p.ref.Start(p.stop)
	return p, nil
}

// Close stops the background refresher.
func (p *Provider) Close() {
	p.stopOnce.Do(func() { close(p.stop) })
}

// reloginsSnapshot returns the current relogin count.
func (p *Provider) reloginsSnapshot() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.relogins
}

// Name returns the provider name.
func (p *Provider) Name() string {
	return "ykt"
}

// Prefix returns the URL path prefix.
func (p *Provider) Prefix() string {
	return "/ykt"
}

// Handler returns the HTTP handler for ykt.
func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()

	// /token - get current token (requires Bearer auth)
	mux.HandleFunc("/token", p.handleToken)

	// /health - health check (requires Bearer auth)
	mux.HandleFunc("/health", p.handleHealth)

	// /api/* - transparent proxy to upstream business APIs (requires Bearer auth)
	mux.HandleFunc("/api/", p.handleProxy)

	// Catch-all 404 for unmapped paths
	mux.HandleFunc("/", p.handle404)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Apply Bearer token validation for /ykt routes
		if !p.validateBearer(w, r) {
			return
		}
		// Route to appropriate handler
		mux.ServeHTTP(w, r)
	})
}

// validateBearer validates Authorization: Bearer token.
func (p *Provider) validateBearer(w http.ResponseWriter, r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		http.Error(w, `{"error":"missing authorization"}`, http.StatusUnauthorized)
		return false
	}

	// Expect "Bearer <key>"
	if len(authHeader) < 7 || authHeader[:7] != "Bearer " {
		http.Error(w, `{"error":"invalid authorization format"}`, http.StatusUnauthorized)
		return false
	}

	token := authHeader[7:]
	if !constantTimeCompare(token, p.cfg.APIKey) {
		http.Error(w, `{"error":"invalid bearer token"}`, http.StatusUnauthorized)
		return false
	}

	return true
}

// handleToken returns the current ykt token.
func (p *Provider) handleToken(w http.ResponseWriter, r *http.Request) {
	// Normally the background refresher keeps the token fresh. The same
	// ensureToken path is used here so request fallback and refresh login
	// attempts are serialized without holding the provider lock during I/O.
	token, err := p.ensureToken(false)
	if err != nil {
		if errors.Is(err, ErrMissingCredential) {
			log.Printf("[ERROR] no cached token and no account/password\n")
			writeJSONError(w, http.StatusServiceUnavailable, "missing credential")
			return
		}
		writeJSONError(w, http.StatusServiceUnavailable, "login failed")
		return
	}

	cache, hasCache := p.session.Snapshot()
	p.mu.Lock()
	p.served++
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if !hasCache || cache.Token != token {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token": token,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"token":          token,
		"expires_at":     cache.ExpiresAt,
		"expires_at_str": cache.ExpiresAtStr,
		"expires_in":     TokenExpiresIn(cache.ExpiresAt),
	})
}

// cachedToken returns a valid cached token without starting a login.
func (p *Provider) cachedToken() (string, bool) {
	cache, ok := p.session.Snapshot()
	if !ok || IsTokenExpiringSoon(cache.ExpiresAt, p.session.refreshMargin) {
		return "", false
	}
	return cache.Token, true
}

// ensureToken returns a valid token. When force is true it always attempts a
// re-login, which is used after the upstream rejects the current token.
// The login itself is serialized, but network I/O never holds p.mu.
func (p *Provider) ensureToken(force bool) (string, error) {
	if !force {
		if token, ok := p.cachedToken(); ok {
			return token, nil
		}
	}

	p.loginMu.Lock()
	defer p.loginMu.Unlock()

	// Another request may have completed the login while we waited.
	if !force {
		if token, ok := p.cachedToken(); ok {
			return token, nil
		}
	}

	if p.cfg.Account == "" || p.cfg.Password == "" {
		return "", ErrMissingCredential
	}

	token, err := p.loginAndStoreOnce()
	if err != nil {
		log.Printf("[WARN] login failed, retrying\n")
		token, err = p.loginAndStoreOnce()
		if err != nil {
			return "", ErrLoginFailed
		}
	}
	return token, nil
}

// loginAndStoreOnce performs one login attempt and updates the cache.
// Caller must hold loginMu. File persistence failures are non-fatal.
func (p *Provider) loginAndStoreOnce() (string, error) {
	token, err := p.login.Login()
	if err != nil {
		return "", err
	}

	if err := p.session.SaveToken(token); err != nil {
		if errors.Is(err, ErrTokenParseFailed) {
			return "", err
		}
		log.Printf("[WARN] failed to persist token cache\n")
	}

	p.mu.Lock()
	p.relogins++
	count := p.relogins
	p.mu.Unlock()
	log.Printf("[INFO] login success, relogins=%d\n", count)
	return token, nil
}

// handleHealth returns health status.
func (p *Provider) handleHealth(w http.ResponseWriter, r *http.Request) {
	cache, hasToken := p.session.Snapshot()
	p.mu.Lock()
	served := p.served
	relogins := p.relogins
	p.mu.Unlock()

	var expiresIn interface{}
	if hasToken {
		expiresIn = TokenExpiresIn(cache.ExpiresAt)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"provider":   "ykt",
		"has_token":  hasToken,
		"expires_in": expiresIn,
		"served":     served,
		"relogins":   relogins,
	})
}

// StatusSnapshot returns the provider status without auth, for the gateway
// /health aggregate. It must not include the token itself or credentials.
func (p *Provider) StatusSnapshot() map[string]interface{} {
	cache, hasToken := p.session.Snapshot()

	status := "ok"
	var expiresIn interface{}
	var expiresAt int64
	if !hasToken {
		status = "no_token"
	} else {
		expiresAt = cache.ExpiresAt
		expiresIn = TokenExpiresIn(cache.ExpiresAt)
		if IsTokenExpiringSoon(cache.ExpiresAt, p.session.refreshMargin) {
			status = "expiring"
		}
	}

	p.mu.Lock()
	served := p.served
	relogins := p.relogins
	p.mu.Unlock()

	return map[string]interface{}{
		"provider":   p.Name(),
		"status":     status,
		"has_token":  hasToken,
		"expires_in": expiresIn,
		"expires_at": expiresAt,
		"served":     served,
		"relogins":   relogins,
	}
}

// handle404 returns 404 for unmapped paths.
func (p *Provider) handle404(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	fmt.Fprint(w, `{"error":"not found"}`)
}

func writeJSONError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

// constantTimeCompare compares two strings in constant time.
func constantTimeCompare(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	result := 0
	for i := 0; i < len(a); i++ {
		result |= int(a[i]) ^ int(b[i])
	}
	return result == 0
}
