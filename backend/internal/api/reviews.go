package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/store"
)

const (
	defaultListLimit = 20
	maxListLimit     = 100
)

// riskCounts 按 severity 统计的风险数；落地"最近评审"卡 + 历史表格的红 / 黄 / 灰 pips 用。
type riskCounts struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

// reviewListItem /api/reviews 列表项；只含 meta + CI + risks 计数，不带完整 payload。
// 给落地"最近评审"卡 + /history 密集表格用，所以体积最小化。
type reviewListItem struct {
	ID         string     `json:"id"`
	Owner      string     `json:"owner"`
	Repo       string     `json:"repo"`
	PR         int        `json:"pr"`
	HeadSHA    string     `json:"head_sha"`
	Title      string     `json:"title,omitempty"`
	CreatedAt  string     `json:"created_at"`
	CI         string     `json:"ci,omitempty"`
	Lang       string     `json:"lang,omitempty"` // PR 主语言（detectPrimaryLang 的结果）；/history 语言筛选用
	Source     string     `json:"source,omitempty"` // "manual" / "webhook"；前端按此渲染 ⚡ chip
	CreatedBy  string     `json:"created_by,omitempty"` // GitHub login；空 = 匿名遗留；前端用来 gate 删除按钮
	RiskCounts riskCounts `json:"risk_counts"`
}

// reviewDetail /api/reviews/:id 详情；完整透出 cachedPayload，
// 给评审页顶栏 + 三视图渲染（review 页缓存秒回路径不再需要回 GitHub 拉 meta）。
type reviewDetail struct {
	reviewListItem
	Author      string          `json:"author,omitempty"`
	AuthorRole  string          `json:"author_role,omitempty"`
	State       string          `json:"state,omitempty"`
	Labels      []string        `json:"labels,omitempty"`
	BaseRef     string          `json:"base_ref,omitempty"`
	HeadRef     string          `json:"head_ref,omitempty"`
	PRCreatedAt time.Time       `json:"pr_created_at,omitzero"`
	Stats       gh.Stats        `json:"stats,omitzero"`
	Checks      []gh.Check      `json:"checks,omitempty"`
	Files        []gh.File            `json:"files,omitempty"` // 给 Diff 视图渲染用；list 端不返该大字段
	Summary      string               `json:"summary"`
	Risks        json.RawMessage      `json:"risks,omitempty"`
	Suggestions  json.RawMessage      `json:"suggestions,omitempty"`
	BudgetReport *budgetReportPayload `json:"budget_report,omitempty"`
}

// countRisksBySeverity 解析 risks_done event raw JSON，按 severity 分组计数。
// 解析失败返零值（容错于格式异常的旧缓存）。
func countRisksBySeverity(raw json.RawMessage) riskCounts {
	if len(raw) == 0 {
		return riskCounts{}
	}
	var items []struct {
		Severity string `json:"severity"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return riskCounts{}
	}
	var c riskCounts
	for _, it := range items {
		switch it.Severity {
		case "high":
			c.High++
		case "medium":
			c.Medium++
		case "low":
			c.Low++
		}
	}
	return c
}

// ListReviews GET /api/reviews?limit=N — 历史评审列表，按 created_at DESC。
// 未配 Store 时返 503 而非 200 空列表，让前端能区分"没有历史"和"功能未启用"。
// 可见性：
//   - 必须登录（匿名访客 401）
//   - 已登录用户 → 只看自己创建的记录
//   - 匿名提交的记录（UserID=nil）：不出现在任何人的列表里，但凭 review URL 仍可直接访问详情
//
// 设计：v2 后评审本身不要求登录（匿名也能评），但匿名记录无 owner 不该出现在任何"历史"语义里
// dev / unit tests 无 Sessions 时 fallback：放行匿名访问 + 列出全部（含 nil UserID），方便本地调试
func ListReviews(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "history disabled: store not configured"})
			return
		}
		// 强制登录：列表里有 PR title / repo / risk 这类内容，不该对匿名访客暴露任何人的提交
		s := CurrentSession(c)
		if d.Sessions != nil && s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录后查看评审历史"})
			return
		}
		limit := parseLimit(c.Query("limit"))
		ctx := c.Request.Context()

		var records []*store.Record
		if s != nil {
			// 登录用户：仅自己创建的；匿名记录从列表中完全隐去
			mine, err := d.Store.List(ctx, &s.Login, limit)
			if err != nil {
				slog.Error("list reviews (mine)", "err", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			records = mine
		} else {
			// dev fallback：无 Sessions 配置时返全部（含匿名），方便本地调试
			all, err := d.Store.List(ctx, nil, limit)
			if err != nil {
				slog.Error("list reviews (dev fallback)", "err", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			records = all
		}
		out := make([]reviewListItem, 0, len(records))
		for _, r := range records {
			it := reviewListItem{
				ID:        r.ID,
				Owner:     r.Owner,
				Repo:      r.Repo,
				PR:        r.PRNumber,
				HeadSHA:   r.HeadSHA,
				CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
			}
			if r.UserID != nil {
				it.CreatedBy = *r.UserID
			}
			// title / ci / risks 计数从 payload 解出；解析失败保留零值（旧缓存兼容）
			var p cachedPayload
			if jerr := json.Unmarshal(r.Payload, &p); jerr == nil {
				it.Title = p.Title
				it.CI = p.CI
				it.Lang = p.Lang
				it.Source = p.Source
				it.RiskCounts = countRisksBySeverity(p.Risks)
			}
			out = append(out, it)
		}
		c.JSON(http.StatusOK, out)
	}
}

// DeleteReview DELETE /api/reviews/:id — 删一条评审记录
// 权限：
//   - 必须已登录
//   - record.UserID == nil（匿名遗留）→ 任何登录用户能删（兼容 v1）
//   - record.UserID 非空 → 只有 owner 自己能删
//
// 不级联删 RAG chunks（chunks 按 owner/repo 共享，删 review 不该影响别的）
func DeleteReview(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "history disabled"})
			return
		}
		s := CurrentSession(c)
		if s == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
			return
		}
		id := c.Param("id")
		rec, err := d.Store.GetByID(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
			return
		}
		// 非匿名遗留 → 只 owner 能删
		if rec.UserID != nil && *rec.UserID != s.Login {
			c.JSON(http.StatusForbidden, gin.H{"error": "只能删除你创建的评审"})
			return
		}
		if err := d.Store.Delete(c.Request.Context(), id); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		// 顺手清掉 agent 追问会话记忆；避免 7 天孤儿驻留 Redis
		// 失败仅 warn——store 已删完整事务，memory 残留只是占点空间
		if d.Memory != nil {
			if mErr := d.Memory.Reset(c.Request.Context(), id); mErr != nil {
				slog.Warn("delete review: reset memory failed", "err", mErr, "id", id)
			}
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	}
}

// GetReview GET /api/reviews/:id — 按 id 取详情；payload 解析失败按 500 暴露而非降级返"空 review"。
func GetReview(d Deps) gin.HandlerFunc {
	return func(c *gin.Context) {
		if d.Store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "history disabled: store not configured"})
			return
		}
		id := c.Param("id")
		rec, err := d.Store.GetByID(c.Request.Context(), id)
		if err != nil {
			slog.Error("get review", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if rec == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
			return
		}
		var p cachedPayload
		if err := json.Unmarshal(rec.Payload, &p); err != nil {
			slog.Error("cached payload unmarshal", "err", err, "id", id)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "corrupted cache payload"})
			return
		}
		// 可见性校验：匿名遗留（UserID==nil）任何人都能看；UserID 非空只有 owner 看
		// 防 URL 猜 ID 偷看别人的私密 review
		if rec.UserID != nil {
			sess := CurrentSession(c)
			if sess == nil || sess.Login != *rec.UserID {
				c.JSON(http.StatusNotFound, gin.H{"error": "review not found"})
				return
			}
		}
		createdBy := ""
		if rec.UserID != nil {
			createdBy = *rec.UserID
		}
		c.JSON(http.StatusOK, reviewDetail{
			reviewListItem: reviewListItem{
				ID:         rec.ID,
				Owner:      rec.Owner,
				Repo:       rec.Repo,
				PR:         rec.PRNumber,
				HeadSHA:    rec.HeadSHA,
				Title:      p.Title,
				CreatedAt:  rec.CreatedAt.UTC().Format(time.RFC3339),
				CI:         p.CI,
				Lang:       p.Lang,
				Source:     p.Source,
				CreatedBy:  createdBy,
				RiskCounts: countRisksBySeverity(p.Risks),
			},
			Files:        p.Files,
			Author:       p.Author,
			AuthorRole:   p.AuthorRole,
			State:        p.State,
			Labels:       p.Labels,
			BaseRef:      p.BaseRef,
			HeadRef:      p.HeadRef,
			PRCreatedAt:  p.PRCreatedAt,
			Stats:        p.Stats,
			Checks:       p.Checks,
			Summary:      p.Summary,
			Risks:        p.Risks,
			Suggestions:  p.Suggestions,
			BudgetReport: p.BudgetReport,
		})
	}
}

// parseLimit 把 query "limit" 解析为 [1, maxListLimit] 内的整数；非法或缺省返 defaultListLimit。
func parseLimit(s string) int {
	if s == "" {
		return defaultListLimit
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultListLimit
	}
	if n > maxListLimit {
		return maxListLimit
	}
	return n
}
