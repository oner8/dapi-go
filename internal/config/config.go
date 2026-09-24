package config

import (
	"bufio"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for dapi-go.
type Config struct {
	// Gateway level
	Listen string // e.g., ":8080"
	APIKey string // optional, for X-Api-Key validation

	// ykt provider
	YKT YKTConfig
}

// YKTConfig holds ykt-specific configuration.
type YKTConfig struct {
	Account         string        // YKT_ACCOUNT
	Password        string        // YKT_PASSWORD
	APIKey          string        // YKT_API_KEY (Bearer token for /ykt/*)
	BaseURL         string        // YKT_BASE_URL, required for serve and login
	TokenFile       string        // YKT_TOKEN_FILE, default ykt-token.json
	RefreshMargin   time.Duration // YKT_REFRESH_MARGIN in seconds, default 300s
	Timeout         time.Duration // YKT_TIMEOUT in seconds, default 20s
	MaxRequestBytes int64         // YKT_MAX_REQUEST_BYTES, default 16 MiB
}

// LoadConfig loads and validates configuration from environment variables.
func LoadConfig() (*Config, error) {
	refreshMargin, err := getEnvInt("YKT_REFRESH_MARGIN", 300)
	if err != nil {
		return nil, err
	}
	timeout, err := getEnvInt("YKT_TIMEOUT", 20)
	if err != nil {
		return nil, err
	}
	maxRequestBytes, err := getEnvInt("YKT_MAX_REQUEST_BYTES", 16<<20)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Listen: strings.TrimSpace(getEnv("API_PROXY_LISTEN", ":8080")),
		APIKey: getEnv("API_PROXY_KEY", ""),
		YKT: YKTConfig{
			Account:         getEnv("YKT_ACCOUNT", ""),
			Password:        getEnv("YKT_PASSWORD", ""),
			APIKey:          getEnv("YKT_API_KEY", ""),
			BaseURL:         strings.TrimRight(strings.TrimSpace(getEnv("YKT_BASE_URL", "")), "/"),
			TokenFile:       strings.TrimSpace(getEnv("YKT_TOKEN_FILE", "ykt-token.json")),
			RefreshMargin:   time.Duration(refreshMargin) * time.Second,
			Timeout:         time.Duration(timeout) * time.Second,
			MaxRequestBytes: int64(maxRequestBytes),
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadConfigFromFile loads configuration from a .env file, overriding with environment variables.
func LoadConfigFromFile(filePath string) (*Config, error) {
	// Load .env file into environment variables without overriding existing values.
	if err := loadEnvFile(filePath); err != nil {
		return nil, err
	}

	return LoadConfig()
}

// validate rejects configuration that would otherwise fail at request time or
// silently disable an important safety limit.
func (c *Config) validate() error {
	if c.Listen == "" {
		return fmt.Errorf("API_PROXY_LISTEN must not be empty")
	}

	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("invalid API_PROXY_LISTEN")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return fmt.Errorf("invalid API_PROXY_LISTEN port")
	}

	if c.APIKey != "" && len(c.APIKey) < 16 {
		return fmt.Errorf("API_PROXY_KEY must be at least 16 characters when set")
	}

	if c.YKT.BaseURL != "" {
		baseURL, err := url.Parse(c.YKT.BaseURL)
		if err != nil || baseURL.Host == "" || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.User != nil || baseURL.Path != "" || baseURL.RawQuery != "" || baseURL.Fragment != "" {
			return fmt.Errorf("YKT_BASE_URL must be an http(s) origin without credentials, path, query or fragment")
		}
	}

	if c.YKT.TokenFile == "" {
		return fmt.Errorf("YKT_TOKEN_FILE must not be empty")
	}
	if c.YKT.RefreshMargin < 0 {
		return fmt.Errorf("YKT_REFRESH_MARGIN must not be negative")
	}
	if c.YKT.Timeout <= 0 || c.YKT.Timeout > 300*time.Second {
		return fmt.Errorf("YKT_TIMEOUT must be greater than 0 and at most 300 seconds")
	}
	if c.YKT.MaxRequestBytes <= 0 || c.YKT.MaxRequestBytes > 256<<20 {
		return fmt.Errorf("YKT_MAX_REQUEST_BYTES must be between 1 and 268435456")
	}

	return nil
}

// loadEnvFile reads a .env file and sets environment variables (only if not already set).
func loadEnvFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to read .env file")
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}

		// Remove quotes if present.
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		} else if commentIdx := strings.Index(value, "#"); commentIdx != -1 {
			// Remove inline comments from unquoted values.
			value = strings.TrimSpace(value[:commentIdx])
		}

		// Only set if not already in environment.
		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("failed to set %s: %w", key, err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to parse .env file")
	}

	return nil
}

func getEnv(key, defaultVal string) string {
	if val, exists := os.LookupEnv(key); exists {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) (int, error) {
	val, exists := os.LookupEnv(key)
	if !exists || strings.TrimSpace(val) == "" {
		return defaultVal, nil
	}

	intVal, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return intVal, nil
}
