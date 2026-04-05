package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Server wraps two http.Server instances: API (localhost) and status (LAN).
type Server struct {
	api    *http.Server
	status *http.Server
}

// Build constructs both servers. The api server binds to 127.0.0.1, the status
// server to 0.0.0.0. Both mux instances come from the same Handler.
func Build(h *Handler, apiPort, statusPort int) *Server {
	mux := h.Routes()
	return &Server{
		api: &http.Server{
			Addr:              fmt.Sprintf("127.0.0.1:%d", apiPort),
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
		status: &http.Server{
			Addr:              fmt.Sprintf("0.0.0.0:%d", statusPort),
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
}

// Start launches both listeners in goroutines. Errors are logged via the
// returned error channel (one error per server at most).
func (s *Server) Start() <-chan error {
	errs := make(chan error, 2)
	go func() {
		log.Printf("[server] API     → http://%s", s.api.Addr)
		if err := s.api.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errs <- fmt.Errorf("api: %w", err)
		}
	}()
	go func() {
		log.Printf("[server] Status  → http://%s", s.status.Addr)
		if err := s.status.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errs <- fmt.Errorf("status: %w", err)
		}
	}()
	return errs
}

// Shutdown triggers a graceful shutdown on both servers.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.api.Shutdown(ctx); err != nil {
		return err
	}
	return s.status.Shutdown(ctx)
}
