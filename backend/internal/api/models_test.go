package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
)

func testRegistry() *llm.Registry {
	p := llm.NewMockProvider()
	return llm.NewRegistry([]llm.ModelProfile{
		{Key: "ds", Label: "DeepSeek", Provider: p, Model: "deepseek-chat"},
		{Key: "gpt", Label: "GPT-4o", Provider: p, Model: "gpt-4o"},
	}, "ds")
}

// GET /api/models returns the selectable models in the registry (the data source for the L3 frontend allowlist).
func TestGetModels_ReturnsRegistryOptions(t *testing.T) {
	srv := startTestServer(t, Deps{Models: testRegistry()})
	resp, err := http.Get(srv.URL + "/api/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}
	var got []llm.ModelOption
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != "ds" || got[1].Key != "gpt" {
		t.Errorf("options=%+v want [ds gpt]", got)
	}
}

// /api/review returns 400 for a model outside the allowlist, before any fetch / LLM call (the allowlist is L3's cost / safety gate).
func TestPostReview_RejectsUnknownModel(t *testing.T) {
	srv := startTestServer(t, Deps{Models: testRegistry()})
	body, _ := json.Marshal(map[string]string{
		"url":   "https://github.com/o/r/pull/1",
		"model": "bogus",
	})
	resp, err := http.Post(srv.URL+"/api/review", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown model 应 400，得到 %d", resp.StatusCode)
	}
}

// an unknown model inside /api/review's stage_models is a 400 too (per-stage overrides go through the same allowlist, before any fetch / LLM call).
func TestPostReview_RejectsUnknownStageModel(t *testing.T) {
	srv := startTestServer(t, Deps{Models: testRegistry()})
	body, _ := json.Marshal(map[string]any{
		"url":          "https://github.com/o/r/pull/1",
		"stage_models": map[string]string{"risks": "bogus"},
	})
	resp, err := http.Post(srv.URL+"/api/review", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("stage_models 含未知模型应 400，得到 %d", resp.StatusCode)
	}
}
