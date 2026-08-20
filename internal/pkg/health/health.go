package health

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

const shutdownBudget = 5 * time.Second

type State struct{ draining atomic.Bool }

func NewState() *State { return &State{} }

func (s *State) StartDraining() { s.draining.Store(true) }

func (s *State) Draining() bool { return s.draining.Load() }

func Handler(s *State) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if s.Draining() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("draining"))
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

type Server struct {
	srv *http.Server
}

func NewServer(addr string, s *State) *Server {
	return &Server{srv: &http.Server{
		Addr:              addr,
		Handler:           Handler(s),
		ReadHeaderTimeout: shutdownBudget,
	}}
}

func (s *Server) Name() string { return "health" }

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownBudget)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	}
}
