package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ecstasoy/LGTM/backend/internal/agent"
	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/memory"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// 允许 steer 重跑的 stage 白名单。summary 重跑收益低（成本高 + 用户主要追问的是风险点 / 建议），暂不开放。
// 实际 Stage 用 newStage 按阶段模型构造（与 mergeStages 同一套 L1 路由）。
var allowedSteerStages = map[string]bool{
	"risks":       true,
	"suggestions": true,
}

// buildAgentUserPrompt 把 prctx.Context 装到 agent 的 user prompt 里。
// 与 stage 模板的核心差异：不要 JSON 输出指令；显式提示 L4 RAG 段是跨 PR 上下文。
// L2 不内联（patch 体积大）—— agent 可调 read_file 按需取；这里只给 L1Meta（含文件名列表）和 L4 召回。
func buildAgentUserPrompt(pr gh.PullRequest, userQuery string, pCtx prctx.Context) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "PR：%s/%s#%d（%s）\n\n", pr.Owner, pr.Repo, pr.Number, pr.Title)
	fmt.Fprintf(&sb, "用户引导：%s\n\n", userQuery)
	sb.WriteString("## PR 元信息\n")
	sb.WriteString(pCtx.L1Meta)
	if pCtx.L3Conventions != "" {
		sb.WriteString("\n\n## 项目约定\n")
		sb.WriteString(pCtx.L3Conventions)
	}
	if len(pCtx.L4References) > 0 {
		sb.WriteString("\n\n## 相关代码（跨文件 RAG 召回；可能来自本 PR 或之前评过的同 repo PR）\n")
		for _, r := range pCtx.L4References {
			origin := r.Reason
			if r.PRNumber > 0 {
				origin = fmt.Sprintf("来自 PR #%d · %s", r.PRNumber, r.Reason)
			}
			fmt.Fprintf(&sb, "\n**%s**（%s）\n```\n%s\n```\n", r.File, origin, r.Snippet)
		}
	}
	return sb.String()
}

// PostSteer POST /api/review/:id/steer
//
// 用户在会话视图底部 SteerComposer 输入引导（如 "重点看并发安全"），从 cached payload 重建 prctx，
// 把引导文本 prepend 到 L1Meta，重新跑指定 stage（默认 risks）；SSE 推 `steered_risks_done` /
// `steered_suggestions_done` 帧 + 终止 done。前端把结果合并到现有 state（替换而非追加）。
//
// 不重新 fetch GitHub：cached files 已经够；首轮的 L3 conventions 没存 cache，重跑时 L3 为空。
// 这是一个 v2 折中：steer 质量略弱于首轮但响应快且不消耗 GitHub API 配额。
func PostSteer(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "steer disabled: store not configured"})
			return
		}

		id := c.Param("id")
		var body struct {
			Text  string `json:"text"`
			Stage string `json:"stage"`
			Mode  string `json:"mode"` // "stage"（默认，重跑 risks/suggestions）/ "agent"（跑 ReAct loop + 工具调用）
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		text := strings.TrimSpace(body.Text)
		if text == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "text is required"})
			return
		}
		mode := body.Mode
		if mode == "" {
			mode = "stage"
		}
		if mode != "stage" && mode != "agent" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mode must be one of: stage, agent"})
			return
		}
		// stage 字段在 mode=stage 时必填；agent 模式忽略
		stageKey := body.Stage
		if stageKey == "" {
			stageKey = "risks"
		}
		stageOK := allowedSteerStages[stageKey]
		if mode == "stage" && !stageOK {
			c.JSON(http.StatusBadRequest, gin.H{"error": "stage must be one of: risks, suggestions"})
			return
		}

		ctx := c.Request.Context()
		rec, err := d.Store.GetByID(ctx, id)
		if err != nil {
			slog.Error("steer get review", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
			return
		}
		var p cachedPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			slog.Error("steer payload unmarshal", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupted cache payload"})
			return
		}

		// 从 cached payload 反向重建一个 PullRequest 给 LayeredBuilder
		pr := gh.PullRequest{
			Owner:     rec.Owner,
			Repo:      rec.Repo,
			Number:    rec.PRNumber,
			HeadSHA:   rec.HeadSHA,
			Title:     p.Title,
			Author:    p.Author,
			State:     p.State,
			Labels:    p.Labels,
			BaseRef:   p.BaseRef,
			HeadRef:   p.HeadRef,
			CreatedAt: p.PRCreatedAt,
			Stats:     p.Stats,
			CI:        p.CI,
			Checks:    p.Checks,
			Files:     p.Files,
			// Conventions 没存 cache；L3 跑出来为空，stage prompt 会少这一段
		}

		builder := d.Builder
		if builder == nil {
			builder = prctx.NewLayeredBuilder()
		}
		// P2: 用用户输入作 RAG query，让追问 / steer 召回更对题（而非默认的 PR 元信息）
		pCtx, err := builder.BuildWith(ctx, pr, prctx.BuildOptions{RAGQuery: text})
		if err != nil {
			slog.Error("steer build prctx", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "build context failed"})
			return
		}

		// 把用户引导 prepend 到 L1Meta，让 prompt 第一眼就看见引导意图
		pCtx.L1Meta = fmt.Sprintf("【用户引导】%s\n\n%s", text, pCtx.L1Meta)

		// SSE headers
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		// mode=agent：跑 ReAct loop + 工具调用；通过 WireAgentSSE 自动推 tool_call_start/done 帧
		if mode == "agent" {
			reg := agent.NewRegistry()
			scope := pr.Owner + "/" + pr.Repo
			agent.RegisterDefaultsWithRAG(reg, p.Files, d.Retriever, scope)

			toolList := "read_file / list_dir / grep_patches"
			hasSearchRepo := false
			if _, ok := reg.Lookup("search_repo"); ok {
				toolList += " / search_repo"
				hasSearchRepo = true
			}

			// 取会话记忆：按 review_id 拉同一对话室之前几轮 user/assistant
			// 错误 fail-soft 为 nil（memory 是增强不是依赖）；turns 顺序 = 时间序最旧在前
			var priorTurns []memory.Turn
			if d.Memory != nil {
				if prior, gerr := d.Memory.Get(ctx, id); gerr != nil {
					slog.Warn("steer load memory failed; running without history", "err", gerr, "id", id)
				} else {
					priorTurns = prior
				}
			}

			writeSSE(c.Writer, "info", map[string]string{
				"message": fmt.Sprintf("Agent 启动：可调用 %s 工具深挖（已记忆 %d 轮对话）", toolList, len(priorTurns)),
				"stage":   "agent",
			})
			c.Writer.Flush()

			a := &agent.Agent{
				Provider: d.Provider,
				Tools:    reg,
				MaxSteps: 8, // 5 过紧；L4 召回不直接命中时给 agent 2-3 次工具迭代余地
			}
			WireAgentSSE(a, c.Writer)

			// 强引导：L4 already has RAG context → 优先据此直答，避免无脑调工具空转
			// PR 沙盒工具 + 可选 search_repo（按 retriever 注入情况浮动）
			sysPrompt := "你是 code reviewer agent。回答 PR 相关问题。\n\n" +
				"## 关键：先看「相关代码」段\n" +
				"prompt 末尾「## 相关代码（跨文件 RAG 召回）」段已是基于用户问题语义召回的本仓库相关代码（可能来自本 PR 也可能来自 main 上未在本 PR 改动的文件）。" +
				"**如果该段已包含足以回答问题的内容，直接基于它给答案，不要调工具。**\n\n" +
				"## 工具\n" +
				"- `read_file` / `list_dir` / `grep_patches`：仅限**本 PR 改动文件**沙盒，跨出会被拒绝。"
			if hasSearchRepo {
				sysPrompt += "\n" +
					"- `search_repo`：在全仓 RAG 索引按 query 语义检索。**只在「相关代码」段不够回答时**才调，并换一个更精准的 query（如具体函数名 / 模块名），避免与初始召回重复。"
			}
			if len(priorTurns) > 0 {
				sysPrompt += "\n\n## 会话历史\n" +
					"用户和你之前已有过多轮对话（下方 messages 含历史 user/assistant 交替）。" +
					"回答时延续上下文 —— 若用户说『那个』『它』『上面提到的』等指代，应解析到历史中的具体对象。"
			}
			sysPrompt += "\n\n## 输出\n" +
				"用一段简洁中文文字回答（不要 JSON）。优先引用具体文件路径 + 行为，让读者能直接定位。"
			userPrompt := buildAgentUserPrompt(pr, text, pCtx)

			// 装载 messages：sys + 历史 (user/assistant 交替) + 当前 user
			// 历史的 tool_calls / observation 不入；agent 需要时会重新调工具
			msgs := []llm.Message{{Role: "system", Content: sysPrompt}}
			for _, t := range priorTurns {
				msgs = append(msgs,
					llm.Message{Role: "user", Content: t.UserText},
					llm.Message{Role: "assistant", Content: t.AgentText},
				)
			}
			msgs = append(msgs, llm.Message{Role: "user", Content: userPrompt})

			result, err := a.Run(ctx, llm.Request{Messages: msgs})
			if err != nil {
				slog.Warn("steer agent run failed", "err", err, "steps", result.Steps)
				// 仅当 agent 没产出任何文字时才推 error；有 Output 时下面 info 帧已能传达
				// 让用户看到具体提示而非 "agent: max steps reached" 这种空话
				if result.Output == "" {
					hint := err.Error()
					if errors.Is(err, agent.ErrMaxStepsReached) {
						hint = fmt.Sprintf("Agent 用尽 %d 步仍未给出答案。"+
							"可能是工具反复访问本 PR 没改的文件。"+
							"试着把问题问得更具体（含文件名 / 函数名），或让我（agent）先看「相关代码」段。", result.Steps)
					}
					writeSSE(c.Writer, "error", map[string]string{
						"stage":   "agent",
						"message": hint,
					})
				}
			}
			// 把 agent 最终输出作 info 帧推给前端（v1 简化：不试图 parse 成 risks/suggestions JSON）
			if result.Output != "" {
				writeSSE(c.Writer, "info", map[string]string{
					"message": fmt.Sprintf("Agent 完成（%d 步）：%s", result.Steps, result.Output),
					"stage":   "agent",
				})
				// 写回记忆：text 是用户原始引导（不含 PR 上下文 / L4 召回），重新装载时 buildAgentUserPrompt 会再拼
				// 只有产出非空才写，避免空回答污染历史
				if d.Memory != nil {
					if mErr := d.Memory.Append(ctx, id, memory.Turn{
						UserText:  text,
						AgentText: result.Output,
						CreatedAt: time.Now(),
						Steps:     result.Steps,
					}); mErr != nil {
						slog.Warn("steer save memory failed; turn lost", "err", mErr, "id", id)
					}
				}
			}
			writeSSE(c.Writer, "done", map[string]any{})
			c.Writer.Flush()
			return
		}

		// 默认 mode=stage：重跑 risks 或 suggestions stage
		writeSSE(c.Writer, "info", map[string]string{
			"message": fmt.Sprintf("正在按引导重跑 %s 阶段…", stageKey),
			"stage":   stageKey,
		})
		c.Writer.Flush()

		// 按阶段经注册表解析 (provider, model)（与 mergeStages 同一套路由）；stageKey 已过白名单校验
		prov, model := resolveProvider(d.Provider, d.Models, d.StageModels[stageKey])
		stage, _ := newStage(stageKey, model)
		events, err := stage.Run(ctx, pCtx, prov)
		if err != nil {
			writeSSE(c.Writer, "error", map[string]string{
				"stage":   "steer:" + stageKey,
				"message": err.Error(),
			})
			writeSSE(c.Writer, "done", map[string]any{})
			return
		}

		// 把 stage 原帧名（risks_done / suggestions_done）转译成 steered_* 推给前端
		// 错误 / done 帧直传
		c.Stream(func(w io.Writer) bool {
			select {
			case <-ctx.Done():
				return false
			case ev, ok := <-events:
				if !ok {
					writeSSERaw(w, "done", json.RawMessage(`{}`))
					return false
				}
				eventType := ev.Type
				switch ev.Type {
				case "risks_done":
					eventType = "steered_risks_done"
				case "suggestions_done":
					eventType = "steered_suggestions_done"
				case "done":
					return true // 跳过 stage 内部 terminal done；上层统一发
				}
				writeSSERaw(w, eventType, ev.Data)
				return true
			}
		})
	}
}

