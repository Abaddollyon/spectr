// Package api provides the Spectr REST API and WebSocket streaming server.
package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Abaddollyon/spectr/pkg/engine"
)

// DefaultPort is the default port for the API server.
const DefaultPort = 9099

// Server is the Spectr REST + WebSocket API server.
type Server struct {
	store     engine.Store
	port      int
	hub       *Hub
	router    chi.Router
	dashFS    http.FileSystem
}

// NewServer creates a new Server with the given store and port.
// If port is 0, DefaultPort is used.
func NewServer(store engine.Store, port int) *Server {
	if port == 0 {
		port = DefaultPort
	}
	s := &Server{
		store: store,
		port:  port,
		hub:   newHub(),
	}
	s.router = s.buildRouter()
	return s
}

// WithDashboard sets the embedded dashboard filesystem.
func (s *Server) WithDashboard(fs http.FileSystem) {
	s.dashFS = fs
	s.router = s.buildRouter()
}

// Hub returns the WebSocket broadcast hub so other components can push events.
func (s *Server) Hub() *Hub {
	return s.hub
}

// ServeHTTP implements http.Handler — used by httptest and embedding.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

// Start runs the HTTP server until ctx is cancelled, then gracefully shuts down.
func (s *Server) Start(ctx context.Context) error {
	go s.hub.run()

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", s.port),
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// buildRouter wires all middleware and routes.
func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(jsonContentType)

		r.Get("/health", s.handleHealth)

		r.Get("/runs", s.handleListRuns)
		r.Get("/runs/{id}", s.handleGetRun)
		r.Get("/runs/{id}/events", s.handleListEvents)
		r.Get("/runs/{id}/tools", s.handleListToolCalls)

		r.Get("/alerts", s.handleListAlerts)
		r.Post("/alerts/{id}/ack", s.handleAckAlert)

		r.Get("/stats/tokens", s.handleTokensOverTime)
		r.Get("/stats/cost", s.handleCostOverTime)
		r.Get("/stats/tools", s.handleTopTools)
	})

	r.Get("/ws/stream", s.handleWebSocket)

	// Dashboard static files (if configured)
	if s.dashFS != nil {
		fileServer := http.FileServer(s.dashFS)
		r.Handle("/*", fileServer)
	}

	return r
}

// jsonContentType sets Content-Type: application/json for non-WebSocket requests.
func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			w.Header().Set("Content-Type", "application/json")
		}
		next.ServeHTTP(w, r)
	})
}
