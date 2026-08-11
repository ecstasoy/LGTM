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

	gh "github.com/ecstasoy/PR-Review-Assistant/backend/internal/github"
	"github.com/ecstasoy/PR-Review-Assistant/backend/internal/index"
	"github.com/ecstasoy/PR-Review-Assistant/backend/internal/llm"
	"github.com/ecstasoy/PR-Review-Assistant/backend/internal/prctx"
	"github.com/ecstasoy/PR-Review-Assistant/backend/internal/review"
	"github.com/ecstasoy/PR-Review-Assistant/backend/internal/store"
)

// indexMaxChunkChars 单 chunk 内容字符上限；超过截断
// embedding API 多数模型上限 8192 token≈30K 字符，留余量
const indexMaxChunkChars = 8000

// splitPatchToHunks 把 unified diff patch 按 `@@ ` hunk 头切成独立片段；每片以 @@ 开头
// 没 @@ 头时（罕见：合并后的预处理）退回整 patch 作单 hunk
// 召回粒度从"一文件一 chunk"细化到"一 hunk 一 chunk"，让 cosine 分更准（噪音少）
func splitPatchToHunks(patch string) []string {
	if !strings.Contains(patch, "@@ ") {
		// fallback：当作单 hunk；空 patch 由 caller 提前 skip
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

// indexPRChunks 把本次 PR 的 file patches 按 hunk 切 chunk 同步写索引
// scope = "owner/repo"；同 (scope,path,idx) ON CONFLICT 覆盖 → 重复评同 PR 不会重复 embed
// idx = 同 path 下 hunk 序号（从 0 开始），让多 hunk 文件不互相覆盖
// 失败仅 warn 不阻断评审；NoopIndexer 直接 no-op 无 API 调用
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

// PostReview 接收 { url }，先用 JSON 处理预检错误；
// 成功后切到 text/event-stream，按帧推送各 stage 事件。
func PostReview(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 评审本身不要登录门槛——demo 零摩擦优先；防滥用靠 expensive rate limit 中间件 + head_sha 缓存
		// 登录用户的 review 归档到名下（per-user 历史 + 删除）；匿名 review 不写 store（不污染列表）
		var creatorLogin string
		if d.Sessions != nil {
			if s := CurrentSession(c); s != nil {
				creatorLogin = s.Login
			}
		}

		var body struct {
			URL   string `json:"url"`
			Model string `json:"model"` // L3：注册表 key，应用到所有阶段；空=默认模型
			// StageModels 按阶段覆盖（summary/risks/suggestions），优先级高于 Model；缺的阶段回退 Model 再回退部署默认
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

		// L3 运行时选模型：每阶段取 stage_models[st] > model（应用到所有阶段）> 部署侧 L1 默认。
		// 每个用户指定的 key 必须在白名单内（成本 / 安全闸）；任一非默认模型则绕过缓存
		// （避免与默认结果串同一缓存槽，也不写入历史；模型维度入缓存键见「下一步」）。
		model := strings.TrimSpace(body.Model)
		stageModels := map[string]string{}
		useCache := true
		for _, st := range []string{"summary", "risks", "suggestions"} {
			picked := strings.TrimSpace(body.StageModels[st])
			if picked == "" {
				picked = model // 回退到「应用到所有阶段」的选择
			}
			if picked == "" {
				stageModels[st] = d.StageModels[st] // 回退部署侧 L1（空 → provider 默认）
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

		// SSE 头：必须在首次 Write 之前设
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no") // 关掉 nginx / 反代缓冲

		// 首帧：PR meta —— 让前端立刻拿到完整顶栏所需字段（CI 圆点 / 作者 / 状态 / 体量 / 分支）
		writeSSE(c.Writer, "pr", prMetaPayload(pr, url))
		c.Writer.Flush()

		// 空 PR 短路：没有可评审的文件改动时，不跑 LLM，直接发 info + done
		if len(pr.Files) == 0 {
			writeSSE(c.Writer, "info", map[string]string{"message": "该 PR 无可评审的文件改动"})
			writeSSE(c.Writer, "done", map[string]any{})
			c.Writer.Flush()
			return
		}

		// 文件列表：让前端立即拿到 Diff 视图所需的文件树 + raw patch，无需等 stage
		writeSSE(c.Writer, "files", pr.Files)
		c.Writer.Flush()

		// 缓存命中：同 (owner, repo, pr, head_sha) 有完整结果直接回放，跳过 LLM
		// 非默认模型 useCache=false → 跳过回放，强制按选中模型重跑
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

		// B4 RAG 同步索引：把本次 PR 的 patches 切 chunks 入库 → Build 时 L4 retrieve 能命中本仓库内容
		// 失败仅 warn（embedding API 抖动 / 配额超），不影响整体评审
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
		// 在跑 LLM 前发预算帧，让前端会话视图可以立刻把上下文步骤切到"已完成"+显示真实 L1/L2/L3/L4
		writeSSE(c.Writer, "budget_report", budget)
		c.Writer.Flush()
		// per-stage RAG query：risks/suggestions 重算 L4（用不同 query），summary 用 baseCtx
		// 多两次 retrieve 调用换更对题的召回；首字节延迟代价 < 200ms
		ctxByStage := buildPerStageContexts(ctx, builder, pr, pCtx)
		merged := mergeStages(ctx, ctxByStage, d.Provider, d.Models, stageModels)

		// 边推流边收集供后续 cache 写入；stage 任一报错则不写缓存（避免缓存半残结果）
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
							// 让流式页前端拿到 ULID 启用「💬 评论 / ✅ 提交 / SteerComposer 追问」按钮
							// （没这帧前端只能等用户回首页点列表条目）
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

// budgetReportPayload 分层 token 预算 SSE 帧 + 缓存 payload 共用形状。
// 与 prctx.BudgetReport 同字段但带 snake_case json tag，避免把内部 pkg 字段名暴露给传输层。
type budgetReportPayload struct {
	TokenLimit int      `json:"token_limit"`
	UsedL1     int      `json:"used_l1"`
	UsedL2     int      `json:"used_l2"`
	UsedL3     int      `json:"used_l3"`
	UsedL4     int      `json:"used_l4,omitempty"`
	Dropped    []string `json:"dropped,omitempty"`
}

// toBudgetPayload 把 prctx.BudgetReport 转成 API 形状；零值安全。
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

// prMetaPayload 把 PR meta 打包成 SSE pr event 的 data。
// 同时被 handler（首帧）和 detail endpoint（缓存命中后给前端兜底头部）共用同一形状。
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

// persistReview 把本次评审序列化后写入 store；缓存写失败仅记日志，不影响响应。
// 用 context.Background() 与请求生命周期解耦：写缓存时客户端可能已断开。
// 返 ID 让 caller emit SSE review_id 帧（前端在流式页面拿到 ULID 后启用 adopt 按钮 / SteerComposer / agent 追问）
// source 标记触发来源："manual"（用户粘 URL）/"webhook"（GitHub 推 PR 自动评）
// createdByLogin GitHub login（manual=当前登录用户 或 "" 匿名；webhook=PR 作者，恒非空）
// 匿名记录（UserID=nil）：可凭 ID URL 访问 + 走缓存命中加速；从 ListReviews 隐去避免污染历史
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
	// UserID 非空字符串 → 真 owner（per-user 可见性 + delete 时校验）
	// 空 → 匿名遗留（任何登录用户都能删，兼容 v1 旧记录）
	if createdByLogin != "" {
		rec.UserID = &createdByLogin
	}
	if err := s.Put(context.Background(), rec); err != nil {
		slog.Error("cache put", "err", err, "owner", pr.Owner, "repo", pr.Repo, "pr", pr.Number)
		return ""
	}
	return rec.ID
}

// stageRAGQueryFor 不同 stage 用不同 RAG query；空返回 = caller fallback 到默认 L1Meta。
// 设计：
//   - summary 看的是全局摘要，PR meta 已经够好；不覆盖
//   - risks 关注 bug/race/security，query 引导召回相关风险代码
//   - suggestions 关注重构/优化，query 引导召回相关 patterns
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

// summarizePRFiles 把改动文件 path 压成一行短串作 RAG query 后缀；只取前 8 个免过长
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

// buildPerStageContexts 为每个 stage 准备 prctx.Context；summary 直接复用 base，risks/suggestions 用各自 query 重算
// 重算失败时回退 base —— RAG 错误不应阻断评审
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

// newStage 按 name 造对应 review.Stage 并注入按阶段模型（model 空串走 provider 默认）。
// ok=false 表示未知 stage 名。供 steer 重跑路径复用同一套按阶段模型路由。
func newStage(name, model string) (review.Stage, bool) {
	switch name {
	case "summary":
		return review.SummaryStage{Model: model}, true
	case "risks":
		return review.RisksStage{Model: model}, true
	case "suggestions":
		return review.SuggestionsStage{Model: model}, true
	default:
		return nil, false
	}
}

// mergeStages 并发跑 summary + risks + suggestions，把各自的事件归并到一个 channel。
// 任一 stage 失败会发一帧 error event 而非中止整条流。
// ctxByStage 按 Stage.Name() 选 prctx.Context；缺失 key 回退到 ctxByStage["summary"]
func mergeStages(ctx context.Context, ctxByStage map[string]prctx.Context, def llm.Provider, models *llm.Registry, stageModels map[string]string) <-chan review.Event {
	merged := make(chan review.Event, 16)
	var wg sync.WaitGroup

	// 每个 stage 经注册表解析自己的 (provider, model)；注册表为 nil 时回退默认 provider（兼容旧调用 / 测试）
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

// resolveProvider 选 stage 的 provider + model：注册表非 nil 时经它解析（支持跨供应商路由），
// 否则回退默认 provider + 把 key 当原始模型名（L1 行为；注册表缺省 / 单元测试时兼容）。
func resolveProvider(def llm.Provider, reg *llm.Registry, key string) (llm.Provider, string) {
	if reg != nil {
		return reg.Resolve(key)
	}
	return def, key
}

// forwardStage 跑一个 stage，把它的事件转发到 merged；ctx 取消时安全退出。
// stage 同步失败时发一帧 error 让前端能感知，而非默默丢失。
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

// writeSSE 在 c.Stream 外部写一帧（首帧 pr meta 用）；调用方负责 Flush。
func writeSSE(w http.ResponseWriter, eventType string, data any) {
	raw, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, raw)
}

// writeSSERaw c.Stream 内部用；payload 已是 json.RawMessage，避免双次 Marshal。
// c.Stream 在 step 返回后自动 Flush。
// Invariant: data must be single-line JSON (no literal newlines); do not pretty-print,
// as embedded newlines would break SSE framing (each data: line must be a complete field).
func writeSSERaw(w io.Writer, eventType string, data json.RawMessage) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data)
}
