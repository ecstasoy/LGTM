package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/index"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/review"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

// indexMaxChunkChars caps a single chunk's content length; anything longer is truncated
// most embedding models cap around 8192 tokens ≈ 30K chars, so leave headroom
const indexMaxChunkChars = 8000

// splitPatchToHunks splits a unified diff patch at each `@@ ` hunk header; every fragment starts with @@
// with no @@ header (rare: post-merge preprocessing) it falls back to the whole patch as one hunk
// retrieval granularity goes from one chunk per file to one chunk per hunk, sharpening cosine scores (less noise)
func splitPatchToHunks(patch string) []string {
	if !strings.Contains(patch, "@@ ") {
		// fallback: treat as a single hunk; empty patches are skipped earlier by the caller
		if strings.TrimSpace(patch) == "" {
			return nil
		}
		return []string{patch}
	}
	var hunks []string
	var cur strings.Builder
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") && cur.Len() > 0 {
			hunks = append(hunks, strings.TrimRight(cur.String(), "\n"))
			cur.Reset()
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	if cur.Len() > 0 {
		hunks = append(hunks, strings.TrimRight(cur.String(), "\n"))
	}
	return hunks
}

// indexPRChunks splits this PR's file patches into hunk chunks and writes them to the index synchronously
// scope = "owner/repo"; the same (scope,path,idx) is overwritten ON CONFLICT, so re-reviewing a PR does not re-embed
// idx = hunk ordinal within a path (0-based), so multi-hunk files do not overwrite each other
// failures only warn, never block the review; NoopIndexer is a no-op with no API calls
func indexPRChunks(ctx context.Context, idx index.Indexer, pr gh.PullRequest) {
	if _, isNoop := idx.(index.NoopIndexer); isNoop {
		return
	}
	chunks := make([]index.IndexerChunk, 0, len(pr.Files))
	for _, f := range pr.Files {
		if f.Patch == "" {
			continue
		}
		for hi, hunk := range splitPatchToHunks(f.Patch) {
			content := hunk
			if len(content) > indexMaxChunkChars {
				content = content[:indexMaxChunkChars]
			}
			chunks = append(chunks, index.IndexerChunk{
				Path:     f.Path,
				Idx:      hi,
				Content:  content,
				PRNumber: pr.Number,
			})
		}
	}
	if len(chunks) == 0 {
		return
	}
	scope := pr.Owner + "/" + pr.Repo
	if err := idx.UpsertMany(ctx, scope, chunks); err != nil {
		slog.Warn("index PR chunks failed; review proceeds without fresh L4", "scope", scope, "err", err)
		return
	}
	slog.Info("indexed PR chunks", "scope", scope, "files", len(pr.Files), "chunks", len(chunks))
}

// PostReview takes { url }, reporting precondition errors as JSON first;
// once past those it switches to text/event-stream and pushes one frame per stage event.
func PostReview(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		// reviewing has no login gate — zero-friction demo first; abuse is handled by the expensive rate limit middleware + head_sha cache
		// a logged-in user's review is archived under their account (per-user history + delete); anonymous reviews skip the store so they do not pollute the list
		var creatorLogin string
		if d.Sessions != nil {
			if s := CurrentSession(c); s != nil {
				creatorLogin = s.Login
			}
		}

		var body struct {
			URL   string `json:"url"`
			Model string `json:"model"` // L3: registry key applied to every stage; empty = default model
			// StageModels overrides per stage (summary/risks/suggestions) and outranks Model; a missing stage falls back to Model, then to the deployment default
			StageModels map[string]string `json:"stage_models"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(400, gin.H{"error": "invalid request body"})
			return
		}
		url := strings.TrimSpace(body.URL)
		if url == "" {
			c.JSON(400, gin.H{"error": "url is required"})
			return
		}

		// L3 runtime model choice: each stage resolves stage_models[st] > model (applied to every stage) > deployment-side L1 default.
		// every user-supplied key must be on the allowlist (cost / safety gate); any non-default model bypasses the cache
		// (so it cannot share a cache slot with the default result, and it is not archived; folding the model into the cache key is future work).
		model := strings.TrimSpace(body.Model)
		stageModels := map[string]string{}
		useCache := true
		for _, st := range []string{"summary", "risks", "suggestions"} {
			picked := strings.TrimSpace(body.StageModels[st])
			if picked == "" {
				picked = model // fall back to the "apply to every stage" choice
			}
			if picked == "" {
				stageModels[st] = d.StageModels[st] // fall back to the deployment-side L1 (empty → provider default)
				continue
			}
			if d.Models != nil && !d.Models.Has(picked) {
				c.JSON(400, gin.H{"error": "未知模型"})
				return
			}
			stageModels[st] = picked
			if d.Models == nil || picked != d.Models.DefaultKey() {
				useCache = false
			}
		}

		ctx := c.Request.Context()

		pr, err := d.Fetcher.Fetch(ctx, url)
		if err != nil {
			switch {
			case errors.Is(err, gh.ErrInvalidPRURL):
				c.JSON(400, gin.H{"error": err.Error()})
			case errors.Is(err, gh.ErrPRNotFound):
				c.JSON(404, gin.H{"error": "PR 不存在或为私有仓库（请配置 GITHUB_TOKEN）"})
			case errors.Is(err, gh.ErrAccessDenied):
				c.JSON(403, gin.H{"error": "GitHub 拒绝访问（速率限制或权限不足）"})
			default:
				slog.Error("fetch PR", "err", err, "url", url)
				c.JSON(502, gin.H{"error": "fetch upstream failed", "detail": err.Error()})
			}
			return
		}

		// SSE headers: must be set before the first Write
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no") // turn off nginx / reverse-proxy buffering

		// first frame: PR meta — gives the frontend every field the top bar needs right away (CI dot / author / state / size / branch)
		writeSSE(c.Writer, "pr", prMetaPayload(pr, url))
		c.Writer.Flush()

		// empty PR short-circuit: with no reviewable file changes, skip the LLM and send info + done directly
		if len(pr.Files) == 0 {
			writeSSE(c.Writer, "info", map[string]string{"message": "该 PR 无可评审的文件改动"})
			writeSSE(c.Writer, "done", map[string]any{})
			c.Writer.Flush()
			return
		}

		// file list: gives the frontend the file tree + raw patch the Diff view needs, without waiting on a stage
		writeSSE(c.Writer, "files", pr.Files)
		c.Writer.Flush()

		// cache hit: a complete result for the same (owner, repo, pr, head_sha) is replayed as-is, skipping the LLM
		// a non-default model sets useCache=false → skip the replay and force a rerun on the chosen model
		if useCache && d.Store != nil {
			if rec, gerr := d.Store.Get(ctx, pr.Owner, pr.Repo, pr.Number, pr.HeadSHA); gerr != nil {
				slog.Warn("cache get failed; falling through to stages", "err", gerr)
			} else if rec != nil {
				var p cachedPayload
				if uerr := json.Unmarshal(rec.Payload, &p); uerr != nil {
					slog.Warn("cached payload unmarshal failed; falling through to stages", "err", uerr, "id", rec.ID)
				} else if p.Risks == nil || p.Suggestions == nil || !json.Valid(p.Risks) || !json.Valid(p.Suggestions) {
					slog.Warn("cached payload incomplete/invalid; falling through to stages", "id", rec.ID)
				} else {
					replayCached(c.Writer, p)
					return
				}
			}
		}

		// RAG indexing, inline: this PR's patches are split into chunks and stored, so L4 retrieve can hit this repo's own content at Build time
		// failures only warn (embedding API flake / quota) and do not affect the review
		if d.Indexer != nil {
			indexPRChunks(ctx, d.Indexer, pr)
		}

		builder := d.Builder
		if builder == nil {
			builder = prctx.NewLayeredBuilder()
		}
		pCtx, err := builder.Build(ctx, pr)
		if err != nil {
			slog.Error("build prompt context", "err", err)
			writeSSE(c.Writer, "error", map[string]string{"stage": "context", "message": err.Error()})
			writeSSE(c.Writer, "done", map[string]any{})
			return
		}
		if len(pCtx.BudgetReport.Dropped) > 0 {
			slog.Warn("prctx dropped large files", "files", pCtx.BudgetReport.Dropped, "limit", pCtx.BudgetReport.TokenLimit)
		}
		budget := toBudgetPayload(pCtx.BudgetReport)
		// send the budget frame before running the LLM, so the session view can flip the context steps to "done" and show the real L1/L2/L3/L4
		writeSSE(c.Writer, "budget_report", budget)
		c.Writer.Flush()
		// per-stage RAG query: risks/suggestions recompute L4 with their own query, summary reuses baseCtx
		// two extra retrieve calls buy more on-topic recall; the cost to first byte is < 200ms
		ctxByStage := buildPerStageContexts(ctx, builder, pr, pCtx)
		merged := mergeStages(ctx, ctxByStage, d.Provider, d.Models, stageModels)

		// collect while streaming so the result can be cached afterwards; any stage error skips the cache write (never cache a half-finished result)
		var (
			summaryBuf       strings.Builder
			risksData        json.RawMessage
			suggestionsData  json.RawMessage
			stageErrObserved bool
		)
		c.Stream(func(w io.Writer) bool {
			select {
			case <-ctx.Done():
				return false
			case ev, ok := <-merged:
				if !ok {
					if useCache && d.Store != nil && !stageErrObserved && risksData != nil && suggestionsData != nil {
						if id := persistReview(d.Store, pr, summaryBuf.String(), risksData, suggestionsData, budget, "manual", creatorLogin); id != "" {
							// lets the streaming page enable the 💬 comment / ✅ commit / SteerComposer follow-up buttons once it has the ULID
							// (without this frame the frontend can only wait for the user to go back and click the list entry)
							raw, _ := json.Marshal(map[string]string{"id": id})
							writeSSERaw(w, "review_id", raw)
						}
					}
					writeSSERaw(w, "done", json.RawMessage(`{}`))
					return false
				}
				switch ev.Type {
				case "summary_delta":
					var p struct {
						Delta string `json:"delta"`
					}
					_ = json.Unmarshal(ev.Data, &p)
					summaryBuf.WriteString(p.Delta)
				case "risks_done":
					risksData = ev.Data
				case "suggestions_done":
					suggestionsData = ev.Data
				case "error":
					stageErrObserved = true
				}
				writeSSERaw(w, ev.Type, ev.Data)
				return true
			}
		})
	}
}

// budgetReportPayload is the shape shared by the layered token budget SSE frame and the cache payload.
// Same fields as prctx.BudgetReport but with snake_case json tags, so internal package field names stay out of the transport layer.
type budgetReportPayload struct {
	TokenLimit int      `json:"token_limit"`
	UsedL1     int      `json:"used_l1"`
	UsedL2     int      `json:"used_l2"`
	UsedL3     int      `json:"used_l3"`
	UsedL4     int      `json:"used_l4,omitempty"`
	Dropped    []string `json:"dropped,omitempty"`
}

// toBudgetPayload converts a prctx.BudgetReport into the API shape; safe on the zero value.
func toBudgetPayload(b prctx.BudgetReport) *budgetReportPayload {
	return &budgetReportPayload{
		TokenLimit: b.TokenLimit,
		UsedL1:     b.UsedL1,
		UsedL2:     b.UsedL2,
		UsedL3:     b.UsedL3,
		UsedL4:     b.UsedL4,
		Dropped:    b.Dropped,
	}
}

// prMetaPayload packs PR meta into the data of the SSE pr event.
// Shared by the handler (first frame) and the detail endpoint (header fallback after a cache hit).
func prMetaPayload(pr gh.PullRequest, sourceURL string) map[string]any {
	payload := map[string]any{
		"id":            pr.HeadSHA,
		"owner":         pr.Owner,
		"repo":          pr.Repo,
		"pr":            pr.Number,
		"url":           sourceURL,
		"head_sha":      pr.HeadSHA,
		"title":         pr.Title,
		"author":        pr.Author,
		"state":         pr.State,
		"labels":        pr.Labels,
		"base_ref":      pr.BaseRef,
		"head_ref":      pr.HeadRef,
		"pr_created_at": pr.CreatedAt,
		"stats":         pr.Stats,
		"ci":            pr.CI,
		"checks":        pr.Checks,
	}
	if pr.AuthorRole != "" {
		payload["author_role"] = pr.AuthorRole
	}
	return payload
}

// persistReview serializes this review into the store; a write failure is only logged and does not affect the response.
// It uses context.Background() to decouple from the request lifecycle: the client may already be gone when the write happens.
// Returns the ID so the caller can emit the SSE review_id frame (with the ULID the streaming page enables the adopt button / SteerComposer / agent follow-up)
// source marks what triggered the review: "manual" (user pasted a URL) / "webhook" (GitHub pushed a PR and it was auto-reviewed)
// createdByLogin is the GitHub login (manual = the logged-in user, or "" when anonymous; webhook = the PR author, always non-empty)
// anonymous records (UserID=nil) stay reachable by ID URL and still hit the cache, but are hidden from ListReviews so they do not pollute history
func persistReview(s store.Store, pr gh.PullRequest, summary string, risks, suggestions json.RawMessage, budget *budgetReportPayload, source string, createdByLogin string) string {
	if source == "" {
		source = "manual"
	}
	payload, err := json.Marshal(cachedPayload{
		Title:        pr.Title,
		Source:       source,
		Files:        pr.Files,
		Author:       pr.Author,
		AuthorRole:   pr.AuthorRole,
		Lang:         detectPrimaryLang(pr.Files),
		State:        pr.State,
		Labels:       pr.Labels,
		BaseRef:      pr.BaseRef,
		HeadRef:      pr.HeadRef,
		PRCreatedAt:  pr.CreatedAt,
		Stats:        pr.Stats,
		CI:           pr.CI,
		Checks:       pr.Checks,
		Summary:      summary,
		Risks:        risks,
		Suggestions:  suggestions,
		BudgetReport: budget,
	})
	if err != nil {
		slog.Error("cache marshal", "err", err)
		return ""
	}
	rec := &store.Record{
		ID:       store.NewID(),
		Owner:    pr.Owner,
		Repo:     pr.Repo,
		PRNumber: pr.Number,
		HeadSHA:  pr.HeadSHA,
		Payload:  payload,
	}
	// a non-empty UserID string → a real owner (per-user visibility + ownership check on delete)
	// empty → an anonymous leftover (any logged-in user may delete it; kept for v1 records)
	if createdByLogin != "" {
		rec.UserID = &createdByLogin
	}
	if err := s.Put(context.Background(), rec); err != nil {
		slog.Error("cache put", "err", err, "owner", pr.Owner, "repo", pr.Repo, "pr", pr.Number)
		return ""
	}
	return rec.ID
}

// stageRAGQueryFor gives each stage its own RAG query; an empty return means the caller falls back to the default L1Meta.
// Rationale:
// - summary wants a global overview and PR meta is already good enough, so it is not overridden
// - risks cares about bugs/races/security, so the query steers recall toward related risky code
// - suggestions cares about refactoring/optimization, so the query steers recall toward related patterns
func stageRAGQueryFor(name string, pr gh.PullRequest) string {
	switch name {
	case "risks":
		return "潜在 bug / 并发 race / 安全漏洞 / 资源泄漏 / 错误处理缺失，相关文件：" + summarizePRFiles(pr.Files)
	case "suggestions":
		return "重构机会 / 代码改进 / 性能优化 / 可读性提升 / 设计模式，相关文件：" + summarizePRFiles(pr.Files)
	default:
		return ""
	}
}

// summarizePRFiles squashes changed file paths into a one-line suffix for the RAG query; capped at the first 8 to keep it short
func summarizePRFiles(files []gh.File) string {
	const maxN = 8
	paths := make([]string, 0, maxN)
	for i, f := range files {
		if i >= maxN {
			break
		}
		paths = append(paths, f.Path)
	}
	return strings.Join(paths, ", ")
}

// buildPerStageContexts prepares a prctx.Context per stage; summary reuses base, risks/suggestions recompute with their own query
// on failure it falls back to base — a RAG error should never block the review
func buildPerStageContexts(
	ctx context.Context,
	builder prctx.Builder,
	pr gh.PullRequest,
	base prctx.Context,
) map[string]prctx.Context {
	out := map[string]prctx.Context{
		"summary": base,
	}
	for _, name := range []string{"risks", "suggestions"} {
		q := stageRAGQueryFor(name, pr)
		if q == "" {
			out[name] = base
			continue
		}
		stageCtx, err := builder.BuildWith(ctx, pr, prctx.BuildOptions{RAGQuery: q})
		if err != nil {
			slog.Warn("per-stage prctx build failed; falling back to base", "stage", name, "err", err)
			out[name] = base
			continue
		}
		out[name] = stageCtx
	}
	return out
}

// newStage builds the review.Stage matching name and injects that stage's model (an empty model means the provider default).
// ok=false means the stage name is unknown. The steer rerun path reuses this same per-stage model routing.
// TODO(next i18n task): Locale is hardcoded to i18n.ZH here to preserve today's all-Chinese behavior; per-request
// locale resolution (body / Accept-Language / DEFAULT_LOCALE) is threaded in by a later task.
func newStage(name, model string) (review.Stage, bool) {
	switch name {
	case "summary":
		return review.SummaryStage{Model: model, Locale: i18n.ZH}, true
	case "risks":
		return review.RisksStage{Model: model, Locale: i18n.ZH}, true
	case "suggestions":
		return review.SuggestionsStage{Model: model, Locale: i18n.ZH}, true
	default:
		return nil, false
	}
}

// mergeStages runs summary + risks + suggestions concurrently and merges their events onto one channel.
// A failing stage emits one error event rather than aborting the whole stream.
// ctxByStage is keyed by Stage.Name(); a missing key falls back to ctxByStage["summary"]
func mergeStages(ctx context.Context, ctxByStage map[string]prctx.Context, def llm.Provider, models *llm.Registry, stageModels map[string]string) <-chan review.Event {
	merged := make(chan review.Event, 16)
	var wg sync.WaitGroup

	// each stage resolves its own (provider, model) through the registry; a nil registry falls back to the default provider (kept for older callers / tests)
	names := []string{"summary", "risks", "suggestions"}
	fallback := ctxByStage["summary"]
	wg.Add(len(names))
	for _, name := range names {
		prov, model := resolveProvider(def, models, stageModels[name])
		stage, _ := newStage(name, model)
		c, ok := ctxByStage[name]
		if !ok {
			c = fallback
		}
		go forwardStage(ctx, c, prov, stage, merged, &wg)
	}

	go func() {
		wg.Wait()
		close(merged)
	}()
	return merged
}

// resolveProvider picks a stage's provider + model: with a non-nil registry it resolves through it (cross-provider routing),
// otherwise it falls back to the default provider and treats the key as a raw model name (L1 behaviour; kept for a missing registry / unit tests).
func resolveProvider(def llm.Provider, reg *llm.Registry, key string) (llm.Provider, string) {
	if reg != nil {
		return reg.Resolve(key)
	}
	return def, key
}

// forwardStage runs one stage and forwards its events onto merged; it exits cleanly when ctx is cancelled.
// A synchronous stage failure emits one error event so the frontend notices, instead of vanishing silently.
func forwardStage(ctx context.Context, c prctx.Context, p llm.Provider, s review.Stage, merged chan<- review.Event, wg *sync.WaitGroup) {
	defer wg.Done()
	events, err := s.Run(ctx, c, p)
	if err != nil {
		payload, _ := json.Marshal(map[string]string{"stage": s.Name(), "message": err.Error()})
		select {
		case <-ctx.Done():
		case merged <- review.Event{Type: "error", Data: payload}:
		}
		return
	}
	for ev := range events {
		if ev.Type == "done" {
			var payload struct {
				Stage string `json:"stage"`
			}
			_ = json.Unmarshal(ev.Data, &payload)
			if payload.Stage != "summary" {
				continue // risks/suggestions terminal done is suppressed; PostReview emits a single terminal done
			}
		}
		select {
		case <-ctx.Done():
			return
		case merged <- ev:
		}
	}
}

// writeSSE writes a frame from outside c.Stream (used for the first pr meta frame); the caller is responsible for Flush.
func writeSSE(w http.ResponseWriter, eventType string, data any) {
	raw, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, raw)
}

// writeSSERaw is for use inside c.Stream; payload is already a json.RawMessage, which avoids marshalling twice.
// c.Stream flushes automatically once step returns.
// Invariant: data must be single-line JSON (no literal newlines); do not pretty-print,
// as embedded newlines would break SSE framing (each data: line must be a complete field).
func writeSSERaw(w io.Writer, eventType string, data json.RawMessage) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}
