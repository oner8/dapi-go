package ykt

import (
	"encoding/json"
	"testing"
	"time"
)

// TestParseToken tests JWT token parsing.
func TestParseToken(t *testing.T) {
	// This is a fixture token with known exp value
	// Token format: "operator" + base64url(zlib({"exp":1234567890,...}))
	// For now, just test error handling

	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{
			name:    "invalid - missing operator prefix",
			token:   "abc.def.ghi",
			wantErr: true,
		},
		{
			name:    "invalid - wrong prefix",
			token:   "xxxxx.def.ghi",
			wantErr: true,
		},
		{
			name:    "invalid - not enough parts",
			token:   "operator.abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseToken(tt.token)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseToken() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDecodeTokenReturnsHeaderAndClaims(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Unix()
	token := makeOperatorToken(t, expiresAt)

	decoded, err := DecodeToken(token)
	if err != nil {
		t.Fatalf("DecodeToken() error = %v", err)
	}
	if decoded.Exp != expiresAt {
		t.Fatalf("Exp = %d, want %d", decoded.Exp, expiresAt)
	}
	if decoded.Iat == 0 {
		t.Fatal("Iat was not decoded")
	}
	if zip, _ := decoded.Header["zip"].(string); zip != "DEF" {
		t.Fatalf("header zip = %#v, want DEF", decoded.Header["zip"])
	}
	if alg, _ := decoded.Header["alg"].(string); alg != "HS512" {
		t.Fatalf("header alg = %#v, want HS512", decoded.Header["alg"])
	}

	payloadJSON, err := json.Marshal(decoded.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !json.Valid(payloadJSON) {
		t.Fatalf("payload is not valid JSON: %s", payloadJSON)
	}
}

// TestTokenExpiresIn tests expiry calculation.
func TestTokenExpiresIn(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name      string
		expiresAt int64
		wantZero  bool
	}{
		{
			name:      "future expiry",
			expiresAt: now + 3600,
			wantZero:  false,
		},
		{
			name:      "past expiry",
			expiresAt: now - 100,
			wantZero:  true, // should return 0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TokenExpiresIn(tt.expiresAt)
			if tt.wantZero && got != 0 {
				t.Errorf("TokenExpiresIn() = %d, want 0 (expired)", got)
			}
			if !tt.wantZero && got <= 0 {
				t.Errorf("TokenExpiresIn() = %d, want > 0", got)
			}
		})
	}
}

// TestIsTokenExpiringSoon tests refresh margin check.
func TestIsTokenExpiringSoon(t *testing.T) {
	now := time.Now().Unix()

	tests := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{
			name:      "expiring soon (within margin)",
			expiresAt: now + 100,
			want:      true,
		},
		{
			name:      "not expiring soon",
			expiresAt: now + 3600,
			want:      false,
		},
		{
			name:      "already expired",
			expiresAt: now - 100,
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsTokenExpiringSoon(tt.expiresAt, 300)
			if got != tt.want {
				t.Errorf("IsTokenExpiringSoon() = %v, want %v", got, tt.want)
			}
		})
	}
}
