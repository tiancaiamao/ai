package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Server is the HTTP RPC server
type Server struct {
	config  *Config
	handler *Handler
	server  *http.Server
	mu      sync.RWMutex
	running bool
}

// Config is the server configuration
type Config struct {
	Host string
	Port int
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		Host: "127.0.0.1",
		Port: 28789,
	}
}

// NewServer creates a new server
func NewServer(cfg *Config) *Server {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Server{
		config:  cfg,
		handler: NewHandler(),
	}
}

// Handler returns the RPC handler for registering methods
func (s *Server) Handler() *Handler {
	return s.handler
}

// Start starts the server
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	mux := http.NewServeMux()
	mux.Handle("/rpc", s.handler)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf("%s:%d", s.config.Host, s.config.Port)
	s.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	s.running = true

	go func() {
		slog.Info("[gateway] Starting RPC server", "addr", addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("[gateway] Server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown server: %w", err)
	}

	s.running = false
	slog.Info("[gateway] RPC server stopped")
	return nil
}

// Running returns whether the server is running
func (s *Server) Running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}