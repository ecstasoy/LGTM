package api

import (
	"context"
	"strings"
	"sync"
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/index"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// recordingProvider records the Model each Stream call receives, to verify per-stage model routing.
type recordingProvider struct {
	mu     sync.Mutex
	models []string
}

func (p *recordingProvider) Stream(_ context.Context, req llm.Request) (<-chan llm.Chunk, error) {
	p.mu.Lock()
	p.models = append(p.models, req.Model)
	p.mu.Unlock()
	ch := make(chan llm.Chunk)
	close(ch) // empty stream: this test only cares whether Model is passed through, not what the stage parses afterwards
	return ch, nil
}

// mergeStages should pass each stage's model through to the provider (L1 per-stage model routing).
func TestMergeStages_RoutesPerStageModels(t *testing.T) {
	p := &recordingProvider{}
	base := prctx.Context{L1Meta: "x"}
	ctxByStage := map[string]prctx.Context{"summary": base, "risks": base, "suggestions": base}
	stageModels := map[string]string{"summary": "m-sum", "risks": "m-risk", "suggestions": "m-sug"}

	for range mergeStages(context.Background(), ctxByStage, p, nil, stageModels, i18n.ZH) {
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

// recordingBuilder passes Build through to base but records the RAGQuery given to BuildWith, so per-stage queries can be asserted as genuinely different
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
		wantSub []string // the query should contain these substrings
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

	// summary must reuse base and never call BuildWith; risks/suggestions must each call it once (with a query)
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

// failingBuilder fails on both Build and BuildWith; verifies buildPerStageContexts falls back to base
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
