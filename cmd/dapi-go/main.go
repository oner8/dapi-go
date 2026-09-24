package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dapi-go/internal/config"
	"dapi-go/internal/gateway"
	"dapi-go/internal/provider"
	"dapi-go/internal/providers/ykt"
)

var (
	envFile = flag.String("env", "", "Path to .env file")
)

func main() {
	flag.Parse()

	// Parse CLI subcommand
	args := flag.Args()
	if len(args) < 1 {
		printUsage()
		os.Exit(1)
	}

	// Load config
	var cfg *config.Config
	var err error
	if *envFile != "" {
		cfg, err = config.LoadConfigFromFile(*envFile)
		if err != nil {
			log.Fatalf("[ERROR] failed to load configuration: %v\n", err)
		}
		log.Printf("[INFO] config loaded from file\n")
	} else {
		cfg, err = config.LoadConfig()
		if err != nil {
			log.Fatalf("[ERROR] invalid configuration: %v\n", err)
		}
		log.Printf("[INFO] config loaded from environment variables\n")
	}

	subcommand := args[0]

	switch subcommand {
	case "serve":
		handleServe(cfg)
	case "login":
		if err := handleLogin(cfg, args[1:]); err != nil {
			log.Fatalf("[ERROR] %v\n", err)
		}
	case "decode":
		if err := handleDecode(cfg, args[1:]); err != nil {
			log.Fatalf("[ERROR] %v\n", err)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", subcommand)
		printUsage()
		os.Exit(1)
	}
}

func handleServe(cfg *config.Config) {
	// Create provider registry
	reg := provider.NewRegistry()

	// Register ykt provider
	yktProvider, err := ykt.NewProvider(cfg.YKT)
	if err != nil {
		log.Fatalf("[ERROR] failed to create ykt provider: %v\n", err)
	}
	defer yktProvider.Close()
	reg.Register(yktProvider)

	log.Printf("[INFO] config loaded providers=ykt\n")

	// Create gateway server
	srv := gateway.NewServer()
	srv.RegisterHandlers(reg, reg.Handlers(), cfg.APIKey)

	// Start server
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.ListenAndServe(cfg.Listen)
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErr:
		if err != nil {
			log.Fatalf("[FATAL] server error: %v\n", err)
		}
	case <-signalCtx.Done():
		log.Printf("[INFO] shutdown signal received\n")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("[ERROR] graceful shutdown failed: %v\n", err)
			return
		}
		if err := <-serverErr; err != nil {
			log.Fatalf("[FATAL] server error during shutdown: %v\n", err)
		}
	}
}

func handleLogin(cfg *config.Config, args []string) error {
	if err := validateProviderArgument(args); err != nil {
		return err
	}

	cache, err := ykt.LoginAndSave(cfg.YKT)
	if err != nil {
		return fmt.Errorf("ykt login failed: %w", err)
	}

	fmt.Printf(
		"login success provider=ykt expires_in=%d expires_at=%d\n",
		ykt.TokenExpiresIn(cache.ExpiresAt),
		cache.ExpiresAt,
	)
	return nil
}

func handleDecode(cfg *config.Config, args []string) error {
	if err := validateProviderArgument(args); err != nil {
		return err
	}

	decoded, err := ykt.DecodeTokenFile(cfg.YKT.TokenFile)
	if err != nil {
		return fmt.Errorf("ykt decode failed: %w", err)
	}

	fmt.Printf("alg: %v\n", decoded.Header["alg"])
	fmt.Printf("zip: %v\n", decoded.Header["zip"])
	fmt.Printf("iat: %s\n", formatUnixTime(decoded.Iat))
	fmt.Printf("exp: %s\n", formatUnixTime(decoded.Exp))
	fmt.Printf("remaining: %s\n", time.Duration(ykt.TokenExpiresIn(decoded.Exp))*time.Second)
	return nil
}

func validateProviderArgument(args []string) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) == 1 && args[0] == "ykt" {
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("expected at most one provider argument")
	}
	return fmt.Errorf("unsupported provider %q (only ykt is available)", args[0])
}

func formatUnixTime(timestamp int64) string {
	if timestamp == 0 {
		return "-"
	}
	return time.Unix(timestamp, 0).Format("2006-01-02 15:04:05")
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: dapi-go [options] <command>

Options:
  -env string    Path to .env file (optional, for local testing)

Commands:
  serve    Start the API gateway server
  login    Force login and save token (ykt only)
  decode   Decode and print JWT from token file (ykt only)

Examples:
  dapi-go serve
  dapi-go -env .env serve
  dapi-go login
  dapi-go decode

Configuration:
  Environment variables (highest priority):
    API_PROXY_LISTEN       Listen address (default :8080)
    API_PROXY_KEY          Optional gateway-level API Key (min 16 chars)
    YKT_ACCOUNT          Account for ykt login
    YKT_PASSWORD         Password for ykt login
    YKT_API_KEY          Bearer token for /ykt/* (required)
    YKT_BASE_URL         ykt upstream URL (required for serve/login)
    YKT_TOKEN_FILE       Path to token cache (default ykt-token.json)
    YKT_REFRESH_MARGIN   Seconds before expiry to refresh (default 300)
    YKT_TIMEOUT          Timeout for ykt requests in seconds, 1-300 (default 20)
    YKT_MAX_REQUEST_BYTES  Max proxied request body size (default 16777216)

  .env file (for local testing):
    Copy .env.example to .env and edit with your values

  Priority order:
    1. Environment variables (override .env file)
    2. .env file (if -env specified)
    3. Built-in defaults

`)
}
