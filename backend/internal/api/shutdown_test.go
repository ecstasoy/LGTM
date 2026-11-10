package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// startServer serves h on a loopback port and returns the server plus its base URL.
func startServer(t *testing.T, h http.Handler) (*http.Server, string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return srv, "http://" + ln.Addr().String()
}

// blockingHandler signals entered once a request arrives, then holds it until release closes.
func blockingHandler(entered, release chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusOK)
	})
}

func getAsync(url string) <-chan error {
	errc := make(chan error, 1)
	go func() {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				err = fmt.Errorf("status %d", resp.StatusCode)
			}
		}
		errc <- err
	}()
	return errc
}

func TestDrain_WaitsForInFlightRequest(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv, url := startServer(t, blockingHandler(entered, release))
	resp := getAsync(url)
	<-entered

	drained := make(chan error, 1)
	go func() { drained <- Drain(context.Background(), srv) }()

	select {
	case err := <-drained:
		t.Fatalf("Drain returned %v while a request was still in flight", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-drained; err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if err := <-resp; err != nil {
		t.Fatalf("in-flight request was cut off: %v", err)
	}
}

func TestDrain_ForceClosesWhenDeadlinePasses(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv, url := startServer(t, blockingHandler(entered, release))
	resp := getAsync(url)
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := Drain(ctx, srv); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Drain err = %v, want context.DeadlineExceeded", err)
	}

	select {
	case err := <-resp:
		if err == nil {
			t.Fatal("request past the deadline completed; want its connection closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request past the deadline is still open; want its connection closed")
	}
}
