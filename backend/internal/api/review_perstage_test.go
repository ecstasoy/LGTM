package api

import (
	"context"
	"strings"
	"sync"
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// recordingProvider 记录每次 Stream 收到的 Model，用于验证 per-stage 模型路由。
type recordingProvider struct {
	mu     sync.Mutex
	models []string
}

func (p *recordingProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	p.mu.Lock()
	p.models = append(p.models, req.Model)
	p.mu.Unlock()
	ch := make(chan llm.Chunk)
	close(ch) // 空流：本测试只关心 Model 是否被透传，不关心 stage 后续解析
	return ch, nil
}

// mergeStages 应把每个 stage 的模型透传给 provider（L1 按阶段模型路由）。
func TestMergeStages_RoutesPerStageModels(t *testing.T) {
	p := &recordingProvider{}
	base := prctx.Context{L1Meta: "x"}
	ctxByStage := map[string]prctx.Context{"summary": base, "risks": base, "suggestions": base}
	stageModels := map[string]string{"summary": "m-sum", "risks": "m-risk", "suggestions": "m-sug"}

	for range mergeStages(context.Background(), ctxByStage, p, nil, stageModels) {
		// drain
	}

	got := map[string]bool{}
	p.mu.Lock()
	for _, m := range p.models {
		got[m] = true
	}
	p.mu.Unlock()
	for _, want := range []string{"m-sum", "m-risk", "m-sug"} {
		if !got[want] {
			t.Errorf("provider 未收到 stage 模型 %q；实际 %v", want, p.models)
		}
	}
}

// recordingBuilder 透传 base Build，但记录 BuildWith 收到的 RAGQuery，便于断言 per-stage 真传了不同 query
type recordingBuilder struct {
	queries []string
}

func (r *recordingBuilder) Build(ctx context.Context, pr gh.PullRequest) (prctx.Context, error) {
	return r.BuildWith(ctx, pr, prctx.BuildOptions{})
}

func (r *recordingBuilder) BuildWith(_ context.Context, _ gh.PullRequest, opts prctx.BuildOptions) (prctx.Context, error) {
	r.queries = append(r.queries, opts.RAGQuery)
	return prctx.Context{L1Meta: "stub"}, nil
}

func TestStageRAGQueryFor(t *testing.T) {
	pr := gh.PullRequest{Files: []gh.File{{Path: "a.go"}, {Path: "b.go"}}}
	cases := []struct {
		stage   string
		wantSub []string // query 应含这些子串
	}{
		{"summary", []string{}},
		{"risks", []string{"bug", "race", "a.go", "b.go"}},
		{"suggestions", []string{"重构", "a.go", "b.go"}},
		{"unknown", []string{}},
	}
	for _, tc := range cases {
		got := stageRAGQueryFor(tc.stage, pr)
		if len(tc.wantSub) == 0 {
			if got != "" {
				t.Errorf("stage=%s: want empty query, got %q", tc.stage, got)
			}
			continue
		}
		for _, sub := range tc.wantSub {
			if !strings.Contains(got, sub) {
				t.Errorf("stage=%s: query missing %q\ngot: %s", tc.stage, sub, got)
			}
		}
	}
}

func TestBuildPerStageContexts_CallsBuildWithQuery(t *testing.T) {
	rb := &recordingBuilder{}
	pr := gh.PullRequest{Files: []gh.File{{Path: "main.go"}}}
	base := prctx.Context{L1Meta: "base"}

	ctxs := buildPerStageContexts(context.Background(), rb, pr, base)

	// summary 必须复用 base 不再调 BuildWith；risks/suggestions 必须各调一次（带 query）
	if got := len(rb.queries); got != 2 {
		t.Fatalf("expected 2 BuildWith calls (risks + suggestions), got %d (queries=%+v)", got, rb.queries)
	}
	for _, q := range rb.queries {
		if q == "" {
			t.Errorf("per-stage query should be non-empty; got: %v", rb.queries)
		}
	}
	if ctxs["summary"].L1Meta != base.L1Meta {
		t.Errorf("summary should reuse base; got %+v", ctxs["summary"])
	}
}

// failingBuilder Build/BuildWith 都报错；验证 buildPerStageContexts fallback 到 base
type failingBuilder struct{}

func (failingBuilder) Build(_ context.Context, _ gh.PullRequest) (prctx.Context, error) {
	return prctx.Context{}, errBuilder
}
func (failingBuilder) BuildWith(_ context.Context, _ gh.PullRequest, _ prctx.BuildOptions) (prctx.Context, error) {
	return prctx.Context{}, errBuilder
}

var errBuilder = stubErr("builder failure")

type stubErr string

func (e stubErr) Error() string { return string(e) }

func TestBuildPerStageContexts_FallsBackOnError(t *testing.T) {
	base := prctx.Context{L1Meta: "the-base", L4References: []index.Reference{{File: "x.go"}}}
	pr := gh.PullRequest{Files: []gh.File{{Path: "a.go"}}}
	ctxs := buildPerStageContexts(context.Background(), failingBuilder{}, pr, base)
	for _, name := range []string{"summary", "risks", "suggestions"} {
		if ctxs[name].L1Meta != "the-base" {
			t.Errorf("stage=%s should fallback to base on err; got %+v", name, ctxs[name])
		}
	}
}
