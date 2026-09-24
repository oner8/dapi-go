package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"dapi-go/internal/provider"
)

// Server is the HTTP gateway server.
type Server struct {
	mux       *http.ServeMux
	registry  *provider.Registry
	startedAt time.Time
	mu        sync.Mutex
	httpSrv   *http.Server
}

// NewServer creates a new gateway server.
func NewServer() *Server {
	return &Server{
		mux:       http.NewServeMux(),
		startedAt: time.Now(),
	}
}

// RegisterHandlers registers all routes and middleware.
func (s *Server) RegisterHandlers(reg *provider.Registry, handlers map[string]http.Handler, apiKey string) {
	s.registry = reg

	// /health - public process probe without provider details
	s.mux.HandleFunc("/health", s.handleHealthz)

	// Provider routes - each provider handles its own paths under its prefix
	for prefix, handler := range handlers {
		// Register with the full prefix pattern
		s.mux.Handle(prefix+"/", s.wrapWithAuth(http.StripPrefix(prefix, handler), apiKey, prefix))
	}

	// Exact root response; unknown paths still return 404.
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"service": "dapi-go",
			"status":  "ok",
			"health":  "/health",
		})
	})
}

// wrapWithAuth wraps a handler with authentication middleware.
// If apiKey is set, validates X-Api-Key. Then lets provider validate its own Bearer token.
func (s *Server) wrapWithAuth(next http.Handler, apiKey, providerPrefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First layer: gateway-level X-Api-Key validation (if set)
		if apiKey != "" {
			xKey := r.Header.Get("X-Api-Key")
			if !constantTimeCompare(xKey, apiKey) {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}

		// Log request (redacted)
		logRequest(r)

		// Let the provider handler do its own Bearer token validation
		next.ServeHTTP(w, r)
	})
}

// handleHealthz returns aggregate health info without auth.
// Read-only view: token values and credentials are never included.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// ListenAndServe starts the server.
func (s *Server) ListenAndServe(addr string) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	s.mu.Lock()
	s.httpSrv = server
	s.mu.Unlock()

	log.Printf("[INFO] server listening on %s\n", addr)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	server := s.httpSrv
	s.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

// constantTimeCompare compares two strings in constant time to prevent timing attacks.
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

// logRequest logs request details (redacted).
func logRequest(r *http.Request) {
	method := r.Method
	log.Printf("[INFO] request method=%s\n", method)
}
