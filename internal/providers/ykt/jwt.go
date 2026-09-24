package ykt

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"io"
	"time"
)

// TokenCache holds the cached token and metadata.
// JSON tags keep the on-disk format lowercase (snake_case) as per DESIGN-001 3.1,
// so external tools (e.g. the Python reference) can read the same file.
type TokenCache struct {
	Token        string `json:"token"`
	SavedAt      int64  `json:"saved_at"`
	ExpiresAt    int64  `json:"expires_at"`
	ExpiresAtStr string `json:"expires_at_str"`
}

// JWTPayload holds the decoded JWT payload.
type JWTPayload struct {
	Exp int64 `json:"exp"`
	Iat int64 `json:"iat"`
	Nbf int64 `json:"nbf"`
}

// DecodedToken contains the decoded JWT header and payload.
type DecodedToken struct {
	Header  map[string]interface{} `json:"header"`
	Payload map[string]interface{} `json:"payload"`
	Exp     int64                  `json:"exp"`
	Iat     int64                  `json:"iat"`
	Nbf     int64                  `json:"nbf"`
}

// ParseToken parses an ykt token (operator prefix + zlib compressed JWT).
// Returns (exp timestamp, error).
func ParseToken(token string) (int64, error) {
	decoded, err := DecodeToken(token)
	if err != nil {
		return 0, ErrTokenParseFailed
	}
	return decoded.Exp, nil
}

// DecodeToken parses the full ykt token for CLI display and token validation.
func DecodeToken(token string) (*DecodedToken, error) {
	// Remove "operator" prefix
	if len(token) < 8 || token[:8] != "operator" {
		return nil, ErrTokenParseFailed
	}
	body := token[8:]

	// Split by "."
	parts := bytes.Split([]byte(body), []byte("."))
	if len(parts) != 3 {
		return nil, ErrTokenParseFailed
	}

	headerJSON, err := decodeTokenPart(parts[0])
	if err != nil {
		return nil, ErrTokenParseFailed
	}
	payloadCompressed, err := decodeTokenPart(parts[1])
	if err != nil {
		return nil, ErrTokenParseFailed
	}

	reader, err := zlib.NewReader(bytes.NewReader(payloadCompressed))
	if err != nil {
		return nil, ErrTokenParseFailed
	}
	defer reader.Close()

	payloadJSON, err := io.ReadAll(io.LimitReader(reader, 1<<20+1))
	if err != nil || len(payloadJSON) > 1<<20 {
		return nil, ErrTokenParseFailed
	}

	var header map[string]interface{}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, ErrTokenParseFailed
	}

	var payload map[string]interface{}
	payloadDecoder := json.NewDecoder(bytes.NewReader(payloadJSON))
	payloadDecoder.UseNumber()
	if err := payloadDecoder.Decode(&payload); err != nil {
		return nil, ErrTokenParseFailed
	}

	var claims JWTPayload
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, ErrTokenParseFailed
	}

	return &DecodedToken{
		Header:  header,
		Payload: payload,
		Exp:     claims.Exp,
		Iat:     claims.Iat,
		Nbf:     claims.Nbf,
	}, nil
}

func decodeTokenPart(part []byte) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(string(bytes.TrimRight(part, "=")))
}

// TokenExpiresIn returns the remaining time until expiry (0 if already expired).
func TokenExpiresIn(expiresAt int64) int64 {
	remaining := expiresAt - time.Now().Unix()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// IsTokenExpiringSoon checks if token expires within margin seconds.
func IsTokenExpiringSoon(expiresAt int64, marginSeconds int64) bool {
	remaining := expiresAt - time.Now().Unix()
	return remaining <= marginSeconds
}
