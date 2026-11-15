package api

import (
	"context"
	"net/http"
	"sync"
)

// Background tracks work that outlives the request that started it, so shutdown can wait for it.
type Background struct{ wg sync.WaitGroup }

// Go runs f in its own goroutine; a nil Background runs it untracked.
func (b *Background) Go(f func()) {
	if b == nil {
		go f()
		return
	}
	b.wg.Go(f)
}

// Wait blocks until all work started by Go has returned, or ctx ends.
func (b *Background) Wait(ctx context.Context) error {
	if b == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Drain stops accepting connections and waits for in-flight requests, then background work, until ctx ends;
// whatever is left at the deadline gets cut off.
func Drain(ctx context.Context, srv *http.Server, bg *Background) error {
	// Requests first: a handler still running can hand more work to bg.
	if err := srv.Shutdown(ctx); err != nil {
		srv.Close()
		return err
	}
	return bg.Wait(ctx)
}
