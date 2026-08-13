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
	"github.com/ecstasoy/LGTM/backend/internal/i18n"
	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/memory"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// agentSystemByLocale carries the localized fragments of the agent's system prompt. steer.go assembles the final
// prompt by concatenating the fragments that apply (search_repo tool present, session history non-empty).
// Kept separate from the stage system-prompt maps in internal/review: this is a different job (multi-turn tool-using
// agent vs. single-shot stage) and its wording will diverge over time.
var agentSystemByLocale = map[i18n.Locale]struct {
	Intro          string
	KeyHint        string
	Tools          string
	SearchRepoTool string
	History        string
	Output         string
}{
	i18n.ZH: {
		Intro: "你是 code reviewer agent。回答 PR 相关问题。\n\n",
		KeyHint: "## 关键：先看「相关代码」段\n" +
			"prompt 末尾「## 相关代码（跨文件 RAG 召回）」段已是基于用户问题语义召回的本仓库相关代码（可能来自本 PR 也可能来自 main 上未在本 PR 改动的文件）。" +
			"**如果该段已包含足以回答问题的内容，直接基于它给答案，不要调工具。**\n\n",
		Tools: "## 工具\n" +
			"- `read_file` / `list_dir` / `grep_patches`：仅限**本 PR 改动文件**沙盒，跨出会被拒绝。",
		SearchRepoTool: "\n" +
			"- `search_repo`：在全仓 RAG 索引按 query 语义检索。**只在「相关代码」段不够回答时**才调，并换一个更精准的 query（如具体函数名 / 模块名），避免与初始召回重复。",
		History: "\n\n## 会话历史\n" +
			"用户和你之前已有过多轮对话（下方 messages 含历史 user/assistant 交替）。" +
			"回答时延续上下文 —— 若用户说『那个』『它』『上面提到的』等指代，应解析到历史中的具体对象。",
		Output: "\n\n## 输出\n" +
			"用一段简洁中文文字回答（不要 JSON）。优先引用具体文件路径 + 行为，让读者能直接定位。",
	},
	i18n.EN: {
		Intro: "You are a code reviewer agent. Answer PR-related questions.\n\n",
		KeyHint: "## Key: check the \"Related code\" section first\n" +
			"The end of the prompt has a \"## Related code (cross-file RAG retrieval)\" section, already retrieved by semantic search against the user's question (it may come from this PR or from files on main that this PR did not touch). " +
			"**If that section already contains enough to answer, answer from it directly — do not call a tool.**\n\n",
		Tools: "## Tools\n" +
			"- `read_file` / `list_dir` / `grep_patches`: sandboxed to **files this PR changed** only; anything outside is rejected.",
		SearchRepoTool: "\n" +
			"- `search_repo`: semantic search over the whole-repo RAG index. **Call it only when the \"Related code\" section is not enough to answer**, and use a sharper query (e.g. a specific function or module name) so it doesn't just repeat the initial retrieval.",
		History: "\n\n## Conversation history\n" +
			"You and the user have already exchanged several turns (the messages below alternate user/assistant history). " +
			"Keep continuity when answering — if the user says \"that\", \"it\", or \"the thing mentioned above\", resolve it to the concrete object from history.",
		Output: "\n\n## Output\n" +
			"Answer in a concise paragraph of English (no JSON). Prefer citing concrete file paths and what happens there, so the reader can go straight to it.",
	},
}

// buildAgentSystemPrompt assembles the agent's system prompt for one locale, appending the SearchRepoTool /
// History fragments only when they apply. Extracted out of PostSteer's inline assembly so it can be
// unit-tested directly, without exercising the full HTTP handler (store, builder, agent registry, SSE writer).
func buildAgentSystemPrompt(locale i18n.Locale, hasSearchRepo, hasHistory bool) string {
	frag := agentSystemByLocale[locale]
	sysPrompt := frag.Intro + frag.KeyHint + frag.Tools
	if hasSearchRepo {
		sysPrompt += frag.SearchRepoTool
	}
	if hasHistory {
		sysPrompt += frag.History
	}
	sysPrompt += frag.Output
	return sysPrompt
}

// Allowlist of stages a steer may rerun. Rerunning summary pays off poorly (expensive, and follow-ups are mostly about risks / suggestions), so it stays closed for now.
// The actual Stage is built by newStage with that stage's model (the same L1 routing as mergeStages).
var allowedSteerStages = map[string]bool{
	"risks":       true,
	"suggestions": true,
}

// buildAgentUserPrompt packs a prctx.Context into the agent's user prompt.
// Key difference from the stage templates: no JSON output instruction, and the L4 RAG section is explicitly flagged as cross-PR context.
// L2 is not inlined (patches are large) — the agent can call read_file on demand; this gives only L1Meta (including the file list) and L4 recall.
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
// The user types a steer in the SteerComposer at the bottom of the session view (e.g. "focus on concurrency safety"); prctx is rebuilt from the cached payload,
// the steer text is prepended to L1Meta, and the named stage is rerun (risks by default). SSE pushes a `steered_risks_done` /
// `steered_suggestions_done` frame plus a terminating done. The frontend merges the result into existing state (replacing, not appending).
//
// GitHub is not re-fetched: the cached files are enough. The first round's L3 conventions were never cached, so L3 is empty on a rerun.
// A deliberate v2 trade-off: steer quality is slightly below the first round, but it responds fast and burns no GitHub API quota.
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
			Mode  string `json:"mode"` // "stage" (default, reruns risks/suggestions) / "agent" (runs the ReAct loop + tool calls)
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
		// stage is required when mode=stage; agent mode ignores it
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

		// rebuild a PullRequest from the cached payload to hand to LayeredBuilder
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
			// Conventions were never cached, so L3 comes back empty and the stage prompt is missing that section
		}

		builder := d.Builder
		if builder == nil {
			builder = prctx.NewLayeredBuilder()
		}
		// P2: use the user's input as the RAG query so follow-ups / steers recall more on-topic material (rather than the default PR metadata)
		pCtx, err := builder.BuildWith(ctx, pr, prctx.BuildOptions{RAGQuery: text})
		if err != nil {
			slog.Error("steer build prctx", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "build context failed"})
			return
		}

		// prepend the user's steer to L1Meta so the prompt sees the intent first thing
		pCtx.L1Meta = fmt.Sprintf("【用户引导】%s\n\n%s", text, pCtx.L1Meta)

		// SSE headers
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		// mode=agent: run the ReAct loop + tool calls; WireAgentSSE pushes the tool_call_start/done frames automatically
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

			// load session memory: pull the previous user/assistant turns of this same room by review_id
			// errors fail soft to nil (memory is an enhancement, not a dependency); turns are ordered oldest first
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
				MaxSteps: 8, // 5 is too tight; when L4 recall misses it leaves the agent 2-3 tool iterations of room
			}
			WireAgentSSE(a, c.Writer)

			// strong steer: L4 already has RAG context → answer from it directly rather than spinning on tool calls
			// PR sandbox tools + optional search_repo (depending on whether a retriever was injected)
			// TODO(next i18n task): resolve locale from the request instead of hardcoding ZH; Deps carries no locale yet.
			locale := i18n.ZH
			sysPrompt := buildAgentSystemPrompt(locale, hasSearchRepo, len(priorTurns) > 0)
			userPrompt := buildAgentUserPrompt(pr, text, pCtx)

			// assemble messages: sys + history (alternating user/assistant) + the current user turn
			// history tool_calls / observations are left out; the agent will call the tools again if it needs them
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
				// only push error when the agent produced no text at all; when there is Output the info frame below already conveys it
				// shows the user something concrete instead of a non-answer like "agent: max steps reached"
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
			// push the agent's final output to the frontend as an info frame (v1 simplification: no attempt to parse it into risks/suggestions JSON)
			if result.Output != "" {
				writeSSE(c.Writer, "info", map[string]string{
					"message": fmt.Sprintf("Agent 完成（%d 步）：%s", result.Steps, result.Output),
					"stage":   "agent",
				})
				// write back to memory: text is the user's raw steer (no PR context / L4 recall), which buildAgentUserPrompt reassembles on the next load
				// only written when the output is non-empty, so blank answers do not pollute history
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

		// default mode=stage: rerun the risks or suggestions stage
		writeSSE(c.Writer, "info", map[string]string{
			"message": fmt.Sprintf("正在按引导重跑 %s 阶段…", stageKey),
			"stage":   stageKey,
		})
		c.Writer.Flush()

		// resolve (provider, model) per stage through the registry (same routing as mergeStages); stageKey has already passed the allowlist check
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

		// translate the stage's own frame names (risks_done / suggestions_done) into steered_* for the frontend
		// error / done frames pass through unchanged
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
					return true // skip the stage's internal terminal done; the layer above sends one
				}
				writeSSERaw(w, eventType, ev.Data)
				return true
			}
		})
	}
}
