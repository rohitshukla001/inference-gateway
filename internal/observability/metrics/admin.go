package metrics

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"
)

// RunAdminServer starts an HTTP server on addr that exposes /metrics from
// m's registry, plus any extra path/handler pairs the caller provides
// (e.g. /health/providers). It blocks until ctx is cancelled and then drains
// in-flight requests before returning.
func RunAdminServer(ctx context.Context, m *Metrics, addr string, extras map[string]http.Handler) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", m.Handler())
	for path, h := range extras {
		mux.Handle(path, h)
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[metrics] admin server listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("admin server: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		<-errCh
		return nil
	case err := <-errCh:
		return err
	}
}
