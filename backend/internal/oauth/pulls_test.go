package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetPullHeadSHA(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/pulls/7" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "unexpected "+r.URL.Path, http.StatusTeapot)
			return
		}
		_, _ = w.Write([]byte(`{"head":{"sha":"feedface"}}`))
	}))
	defer srv.Close()
	c := &Client{HTTPClient: &http.Client{Transport: rewriteHost{base: srv.URL}}}

	sha, err := c.GetPullHeadSHA(context.Background(), "tok", "o", "r", 7)
	if err != nil || sha != "feedface" {
		t.Fatalf("got %q, %v", sha, err)
	}
}

func TestGetPullHeadSHA_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	c := &Client{HTTPClient: &http.Client{Transport: rewriteHost{base: srv.URL}}}

	if _, err := c.GetPullHeadSHA(context.Background(), "tok", "o", "r", 7); err == nil {
		t.Fatal("want an error on 404")
	}
}
