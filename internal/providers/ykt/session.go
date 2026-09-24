package ykt

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionManager manages token caching and persistence.
type SessionManager struct {
	tokenCache    *TokenCache
	filePath      string
	refreshMargin int64 // seconds
	mu            sync.Mutex
}

// NewSessionManager creates a new session manager.
func NewSessionManager(filePath string, refreshMarginSeconds int64) *SessionManager {
	sm := &SessionManager{
		filePath:      filePath,
		refreshMargin: refreshMarginSeconds,
	}
	// Try to load cached token
	sm.loadFromFile()
	return sm
}

// GetToken returns the current valid token, or error if cannot get one.
// If token is expiring soon or missing, it returns error (caller should login).
func (sm *SessionManager) GetToken() (string, bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.tokenCache == nil {
		return "", false, ErrMissingCredential // need to login
	}

	// Check if expiring soon
	if IsTokenExpiringSoon(sm.tokenCache.ExpiresAt, sm.refreshMargin) {
		return "", false, nil // need to refresh (caller should login)
	}

	return sm.tokenCache.Token, true, nil
}

// SaveToken saves the token to cache and file.
func (sm *SessionManager) SaveToken(token string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	expiresAt, err := ParseToken(token)
	if err != nil {
		return err
	}

	cache := &TokenCache{
		Token:        token,
		SavedAt:      time.Now().Unix(),
		ExpiresAt:    expiresAt,
		ExpiresAtStr: time.Unix(expiresAt, 0).Format("2006-01-02 15:04:05"),
	}

	// Update in-memory cache
	sm.tokenCache = cache

	// Persist to file
	if err := sm.saveToFile(cache); err != nil {
		log.Printf("[WARN] failed to save token to file\n")
		return fmt.Errorf("failed to persist token: %w", err)
	}

	return nil
}

// Snapshot returns a copy of the cached token and whether it exists.
func (sm *SessionManager) Snapshot() (TokenCache, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.tokenCache == nil {
		return TokenCache{}, false
	}
	return *sm.tokenCache, true
}

// loadFromFile attempts to load token from file.
// Accepts both the new lowercase format and the legacy capitalized field
// names (Token/ExpiresAt) written by older builds without JSON tags.
func (sm *SessionManager) loadFromFile() {
	data, err := os.ReadFile(sm.filePath)
	if err != nil {
		// File doesn't exist or can't be read - OK, no cached token
		return
	}

	// Unmarshal into a map first so we can normalize legacy capitalized keys.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("[WARN] failed to parse token file: %v\n", err)
		return
	}

	legacy := false
	for newKey, oldKey := range map[string]string{
		"token":          "Token",
		"saved_at":       "SavedAt",
		"expires_at":     "ExpiresAt",
		"expires_at_str": "ExpiresAtStr",
	} {
		if v, ok := raw[newKey]; !ok || v == nil {
			if v2, ok2 := raw[oldKey]; ok2 && v2 != nil {
				raw[newKey] = v2
				legacy = true
			}
		}
	}

	normalized, err := json.Marshal(raw)
	if err != nil {
		log.Printf("[WARN] failed to normalize token file: %v\n", err)
		return
	}

	var cache TokenCache
	if err := json.Unmarshal(normalized, &cache); err != nil {
		log.Printf("[WARN] failed to parse token file: %v\n", err)
		return
	}
	if cache.Token == "" {
		log.Printf("[WARN] token file has no token field, ignoring\n")
		return
	}
	expiresAt, err := ParseToken(cache.Token)
	if err != nil {
		log.Printf("[WARN] token file contains an invalid token\n")
		return
	}
	cache.ExpiresAt = expiresAt
	cache.ExpiresAtStr = time.Unix(expiresAt, 0).Format("2006-01-02 15:04:05")

	if legacy {
		log.Printf("[INFO] legacy token file format detected (capitalized keys), migrating on next save\n")
	}

	sm.tokenCache = &cache
	log.Printf("[DEBUG] loaded cached token, expires at %s\n", cache.ExpiresAtStr)
}

// saveToFile persists token to file atomically (temp + rename).
func (sm *SessionManager) saveToFile(cache *TokenCache) error {
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(sm.filePath)
	pattern := "." + filepath.Base(sm.filePath) + ".tmp-*"
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0600); err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, sm.filePath); err != nil {
		return err
	}
	cleanup = false

	log.Printf("[DEBUG] saved token cache\n")
	return nil
}
