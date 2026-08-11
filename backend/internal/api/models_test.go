package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ecstasoy/PR-Review-Assistant/backend/internal/llm"
)

func testRegistry() *llm.Registry {
	p := llm.NewMockProvider()
	return llm.NewRegistry([]llm.ModelProfile{
		{Key: "ds", Label: "DeepSeek", Provider: p, Model: "deepseek-chat"},
		{Key: "gpt", Label: "GPT-4o", Provider: p, Model: "gpt-4o"},
	}, "ds")
}

// GET /api/models 返回注册表里的可选模型（L3 前端白名单数据源）。
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

// /api/review 收到不在白名单的 model 时 400，不进入 fetch / LLM（白名单是 L3 的成本 / 安全闸）。
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

// /api/review 的 stage_models 里有未知模型时也 400（按阶段覆盖同样走白名单，不进 fetch / LLM）。
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
