package api

import (
	"context"
	"net/http"
)

// Drain stops accepting connections and waits for in-flight requests until ctx ends, then force-closes the rest.
func Drain(ctx context.Context, srv *http.Server) error {
	if err := srv.Shutdown(ctx); err != nil {
		srv.Close()
		return err
	}
	return nil
}
