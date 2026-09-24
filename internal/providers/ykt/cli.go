package ykt

import (
	"fmt"
	"strings"

	"dapi-go/internal/config"
)

// LoginAndSave performs the CLI login flow and persists the returned token.
// It mirrors the service login retry policy: two total attempts.
func LoginAndSave(cfg config.YKTConfig) (*TokenCache, error) {
	if cfg.BaseURL == "" {
		return nil, ErrMissingBaseURL
	}
	if cfg.Account == "" || cfg.Password == "" {
		return nil, ErrMissingCredential
	}
	if strings.TrimSpace(cfg.TokenFile) == "" {
		return nil, fmt.Errorf("YKT_TOKEN_FILE must not be empty")
	}

	client := NewLoginClient(cfg.BaseURL, cfg.Account, cfg.Password, cfg.Timeout)
	for attempt := 0; attempt < 2; attempt++ {
		token, err := client.Login()
		if err != nil {
			continue
		}

		session := NewSessionManager(cfg.TokenFile, int64(cfg.RefreshMargin.Seconds()))
		if err := session.SaveToken(token); err != nil {
			return nil, fmt.Errorf("save token: %w", err)
		}
		cache, ok := session.Snapshot()
		if !ok {
			return nil, ErrTokenParseFailed
		}
		return &cache, nil
	}

	return nil, ErrLoginFailed
}

// DecodeTokenFile loads the token cache and decodes its token for CLI display.
func DecodeTokenFile(filePath string) (*DecodedToken, error) {
	if strings.TrimSpace(filePath) == "" {
		return nil, fmt.Errorf("YKT_TOKEN_FILE must not be empty")
	}

	session := NewSessionManager(filePath, 0)
	cache, ok := session.Snapshot()
	if !ok {
		return nil, fmt.Errorf("no cached token")
	}
	return DecodeToken(cache.Token)
}
