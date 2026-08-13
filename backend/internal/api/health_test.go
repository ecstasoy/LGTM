package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// failPingStore decorates a Store to make Ping fail; every other method delegates to inner
type failPingStore struct{ inner store.Store }

func (s failPingStore) Get(ctx context.Context, owner, repo string, pr int, headSHA, locale string) (*store.Record, error) {
	return s.inner.Get(ctx, owner, repo, pr, headSHA, locale)
}
func (s failPingStore) Put(ctx context.Context, r *store.Record) error { return s.inner.Put(ctx, r) }
func (s failPingStore) List(ctx context.Context, userID *string, limit int) ([]*store.Record, error) {
	return s.inner.List(ctx, userID, limit)
}
func (s failPingStore) GetByID(ctx context.Context, id string) (*store.Record, error) {
	return s.inner.GetByID(ctx, id)
}
func (s failPingStore) Delete(ctx context.Context, id string) error {
	return s.inner.Delete(ctx, id)
}
func (s failPingStore) Ping(ctx context.Context) error { return errors.New("synthetic failure") }

func TestHealth_LivenessAlwaysOK(t *testing.T) {
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider()})
	res, _ := getJSON(t, srv, "/api/health")
	if res.StatusCode != 200 {
		t.Errorf("/api/health want 200 got %d", res.StatusCode)
	}
	res2, _ := getJSON(t, srv, "/api/health/live")
	if res2.StatusCode != 200 {
		t.Errorf("/api/health/live want 200 got %d", res2.StatusCode)
	}
}

func TestReadiness_StoreHealthy_200(t *testing.T) {
	s := newTestStore(t)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: s})
	res, body := getJSON(t, srv, "/api/health/ready")
	if res.StatusCode != 200 {
		t.Fatalf("want 200 got %d body=%s", res.StatusCode, body)
	}
	var v struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	_ = json.Unmarshal([]byte(body), &v)
	if v.Status != "ready" || v.Checks["store"] != "ok" {
		t.Errorf("want ready+store:ok, got %+v", v)
	}
}

func TestReadiness_StorePingFails_503(t *testing.T) {
	s := newTestStore(t)
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: failPingStore{inner: s}})
	res, body := getJSON(t, srv, "/api/health/ready")
	if res.StatusCode != 503 {
		t.Fatalf("want 503 got %d body=%s", res.StatusCode, body)
	}
	var v struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	_ = json.Unmarshal([]byte(body), &v)
	if v.Status != "degraded" {
		t.Errorf("want degraded, got %q", v.Status)
	}
}

func TestReadiness_NoStore_200WithDisabled(t *testing.T) {
	srv := startTestServer(t, Deps{Provider: llm.NewMockProvider(), Store: nil})
	res, body := getJSON(t, srv, "/api/health/ready")
	if res.StatusCode != 200 {
		t.Fatalf("want 200 (no deps to fail) got %d", res.StatusCode)
	}
	var v struct {
		Checks map[string]string `json:"checks"`
	}
	_ = json.Unmarshal([]byte(body), &v)
	if v.Checks["store"] != "disabled" {
		t.Errorf("want store=disabled, got %v", v.Checks)
	}
}
