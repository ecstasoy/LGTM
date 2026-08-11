package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/oauth"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
	"github.com/ecstasoy/LGTM/backend/internal/review"
)

// WebhookPR GitHub pull_request webhook payload 字段子集
// 只取触发评审需要的；忽略 base/head ref 等大段
type WebhookPR struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		HeadSHA string `json:"-"` // 后端 Fetch 时拿
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"pull_request"`
	Repository struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// WebhookIssueComment issue_comment 事件 payload；PR 在 GitHub 也算 Issue
// issue.pull_request 字段非空 → 该 comment 是发在 PR 上
type WebhookIssueComment struct {
	Action string `json:"action"`
	Issue  struct {
		Number      int    `json:"number"`
		HTMLURL     string `json:"html_url"`
		Title       string `json:"title"`
		User        struct {
			Login string `json:"login"` // PR 作者；slash 触发时也通知 ta
		} `json:"user"`
		PullRequest *struct {
			URL     string `json:"url"`      // API URL
			HTMLURL string `json:"html_url"` // 用户面 URL
		} `json:"pull_request"`
	} `json:"issue"`
	Comment struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"comment"`
	Repository struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Sender struct {
		Login string `json:"login"`
	} `json:"sender"`
}

// allowedPRActions PR 事件中触发自动评审的 action 白名单
// opened: 新 PR；synchronize: push 新 commit（head_sha 改）；reopened: 重开
// 其它 action（closed / merged / labeled / ...）不触发
var allowedPRActions = map[string]bool{
	"opened":      true,
	"synchronize": true,
	"reopened":    true,
}

// WebhookGitHub POST /api/webhook/github
//
// 安全：HMAC-SHA256 校验 X-Hub-Signature-256（用 GITHUB_APP_WEBHOOK_SECRET）
// 事件路由：仅 pull_request.opened 触发；其它 204
// 异步：respond 200 立刻；review 在 goroutine 跑（10-30s，超 GitHub 10s 重试阈值）
//
// 幂等：同 (owner, repo, pr, head_sha) 已有 review → 跳过；仅 push 通知
func WebhookGitHub(d Deps, webhookSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
			return
		}

		// HMAC 校验：sha256=<hex>
		sig := c.GetHeader("X-Hub-Signature-256")
		if webhookSecret == "" {
			slog.Warn("webhook: WEBHOOK_SECRET 未配；拒绝以防伪造")
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "webhook secret not configured"})
			return
		}
		if !verifyHMAC(sig, body, []byte(webhookSecret)) {
			slog.Warn("webhook: HMAC mismatch", "sig_prefix", safePrefix(sig))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "signature mismatch"})
			return
		}

		event := c.GetHeader("X-GitHub-Event")
		switch event {
		case "pull_request":
			handlePullRequestEvent(c, d, body)
		case "issue_comment":
			handleIssueCommentEvent(c, d, body)
		case "ping":
			c.JSON(http.StatusOK, gin.H{"ok": true, "pong": true})
		default:
			c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": event})
		}
	}
}

// handlePullRequestEvent opened / synchronize / reopened 都触发评审
func handlePullRequestEvent(c *gin.Context, d Deps, body []byte) {
	var p WebhookPR
	if err := json.Unmarshal(body, &p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse: " + err.Error()})
		return
	}
	if !allowedPRActions[p.Action] {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored_action": p.Action})
		return
	}
	if p.PullRequest.HTMLURL == "" || p.Repository.Owner.Login == "" || p.Repository.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payload missing required fields"})
		return
	}

	slog.Info("webhook: pull_request event received",
		"action", p.Action,
		"owner", p.Repository.Owner.Login, "repo", p.Repository.Name,
		"pr", p.Number, "sender", p.Sender.Login, "installation", p.Installation.ID)

	c.JSON(http.StatusAccepted, gin.H{"ok": true, "queued": true, "action": p.Action})

	go runWebhookReview(d, webhookReviewArgs{
		PrURL:          p.PullRequest.HTMLURL,
		Owner:          p.Repository.Owner.Login,
		Repo:           p.Repository.Name,
		Number:         p.Number,
		Title:          p.PullRequest.Title,
		InstallationID: p.Installation.ID,
		SenderLogin:    p.Sender.Login,
		// PR author 也通知一份（synchronize 时 sender=pusher 可能不是 author）
		// 同人时 PushNotification 内部 dedupe（其实不会，所以 caller 负责）
		PRAuthorLogin: p.PullRequest.User.Login,
		TriggerAction: p.Action, // 给 bot review body 用（区分新评 vs 同步重评）
	})
}

// handleIssueCommentEvent 解析 PR 评论里的 /lgtm <cmd> slash command
// 触发条件：action=created + issue 是 PR + 评论体首行 /lgtm review
// 防 bot 自己回评导致循环：sender.login 含 [bot] 后缀直接忽略
func handleIssueCommentEvent(c *gin.Context, d Deps, body []byte) {
	var p WebhookIssueComment
	if err := json.Unmarshal(body, &p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "parse: " + err.Error()})
		return
	}
	if p.Action != "created" {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored_action": p.Action})
		return
	}
	// PR comment only —— issue.pull_request 字段非空才是 PR
	if p.Issue.PullRequest == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": "non-PR issue comment"})
		return
	}
	// 防自我循环：bot 自己的回复也是 issue_comment，会再来一次 webhook
	if strings.HasSuffix(p.Sender.Login, "[bot]") {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": "bot sender"})
		return
	}

	cmd := parseSlashCommand(p.Comment.Body)
	if cmd == "" {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": "no /lgtm command"})
		return
	}

	slog.Info("webhook: slash command received",
		"cmd", cmd, "owner", p.Repository.Owner.Login, "repo", p.Repository.Name,
		"pr", p.Issue.Number, "sender", p.Sender.Login)

	c.JSON(http.StatusAccepted, gin.H{"ok": true, "queued": true, "cmd": cmd})

	switch cmd {
	case "review":
		go runSlashReview(d, slashReviewArgs{
			PrURL:          p.Issue.PullRequest.HTMLURL,
			Owner:          p.Repository.Owner.Login,
			Repo:           p.Repository.Name,
			Number:         p.Issue.Number,
			Title:          p.Issue.Title,
			InstallationID: p.Installation.ID,
			SenderLogin:    p.Sender.Login,
			PRAuthorLogin:  p.Issue.User.Login,
		})
	case "help":
		go runSlashHelp(d, p.Repository.Owner.Login, p.Repository.Name, p.Issue.Number, p.Installation.ID)
	default:
		// 不识别的命令也回个 ack，提示可用列表
		go runSlashHelp(d, p.Repository.Owner.Login, p.Repository.Name, p.Issue.Number, p.Installation.ID)
	}
}

// parseSlashCommand 找 body 里第一行 /lgtm <cmd>，返 <cmd>；无返 ""
// 大小写不敏感命令；忽略命令前空白
func parseSlashCommand(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "/lgtm") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "/lgtm"))
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return "review" // 裸 /lgtm 默认 review
		}
		return strings.ToLower(fields[0])
	}
	return ""
}

// runSlashReview slash command 触发的重评；先 post ack comment，再跑评审
func runSlashReview(d Deps, args slashReviewArgs) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// ack 评论用 installation token；让用户立刻看到「LGTM 已收到」
	if d.OAuthClient != nil && d.OAuthClient.AppID != 0 && len(d.OAuthClient.PrivateKeyPEM) > 0 && args.InstallationID != 0 {
		jwt, err := oauth.AppJWT(d.OAuthClient.AppID, d.OAuthClient.PrivateKeyPEM)
		if err != nil {
			slog.Warn("slash review: app jwt failed", "err", err)
		} else if tok, err := d.OAuthClient.GetInstallationToken(ctx, jwt, args.InstallationID); err != nil {
			slog.Warn("slash review: installation token failed", "err", err)
		} else {
			_, _ = d.OAuthClient.PostIssueComment(ctx, tok.Token, args.Owner, args.Repo, args.Number,
				"🤖 LGTM 已收到 `/lgtm review`，正在评审…（约 30s 后回贴完整 review）")
		}
	}

	// 复用 runWebhookReview 的全套：fetch → 幂等检查 → index → review → bot review post
	runWebhookReview(d, webhookReviewArgs{
		PrURL:          args.PrURL,
		Owner:          args.Owner,
		Repo:           args.Repo,
		Number:         args.Number,
		Title:          args.Title,
		InstallationID: args.InstallationID,
		SenderLogin:    args.SenderLogin,
		PRAuthorLogin:  args.PRAuthorLogin,
		TriggerAction:  "slash_review",
	})
}

// runSlashHelp 不识别的命令时给用户列出可用命令
func runSlashHelp(d Deps, owner, repo string, prNumber int, installationID int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if d.OAuthClient == nil || d.OAuthClient.AppID == 0 || len(d.OAuthClient.PrivateKeyPEM) == 0 || installationID == 0 {
		return
	}
	jwt, err := oauth.AppJWT(d.OAuthClient.AppID, d.OAuthClient.PrivateKeyPEM)
	if err != nil {
		return
	}
	tok, err := d.OAuthClient.GetInstallationToken(ctx, jwt, installationID)
	if err != nil {
		return
	}
	body := "🤖 **LGTM 可用命令**\n\n" +
		"- `/lgtm review` —— 重新评审当前 PR（同 push 新 commit 自动触发的逻辑）\n" +
		"- `/lgtm help` —— 显示本帮助\n\n" +
		"<sub>更多命令开发中（如 `/lgtm explain <file>:<line>` 单独解释某行）</sub>"
	_, _ = d.OAuthClient.PostIssueComment(ctx, tok.Token, owner, repo, prNumber, body)
}

type slashReviewArgs struct {
	PrURL          string
	Owner          string
	Repo           string
	Number         int
	Title          string
	InstallationID int64
	SenderLogin    string
	PRAuthorLogin  string
}

// uniqueRecipients 通知收件人去重：空字符串过滤；同名只保留一份
// 用于 PR author == sender 的常见情况（opened 时一致）
func uniqueRecipients(logins ...string) []string {
	seen := make(map[string]bool, len(logins))
	out := make([]string, 0, len(logins))
	for _, l := range logins {
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

type webhookReviewArgs struct {
	PrURL          string
	Owner          string
	Repo           string
	Number         int
	Title          string
	InstallationID int64
	// SenderLogin 触发本次事件的 GitHub 用户（push 的人 / 评论 /lgtm 的人）
	SenderLogin string
	// PRAuthorLogin PR 作者；通常跟 SenderLogin 同（opened 时一致），
	// 但 synchronize 时 sender=pusher 可能不是 author；slash 时 sender=commenter 也不一定是 author
	// 通知两个人都推一份（caller 内部 dedupe）确保 PR 作者总能收到
	PRAuthorLogin string
	// TriggerAction "opened" / "synchronize" / "reopened" / "slash_review"；
	// 用来在 bot review body 里区分文案（"已重评 push 的最新 commit" vs "首次评审"）
	TriggerAction string
}

// runWebhookReview 后台跑评审 + 持久化 + push 通知 + post bot review
// 任一步失败 log 但不重试（demo 简化；prod 加 queue + retry）
func runWebhookReview(d Deps, args webhookReviewArgs) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pr, err := d.Fetcher.Fetch(ctx, args.PrURL)
	if err != nil {
		slog.Error("webhook: fetch PR failed", "err", err, "url", args.PrURL)
		return
	}

	// 幂等检查：同 (owner, repo, pr, head_sha) 已评 → 跳过
	if d.Store != nil {
		if rec, _ := d.Store.Get(ctx, pr.Owner, pr.Repo, pr.Number, pr.HeadSHA); rec != nil {
			slog.Info("webhook: cache hit, skipping re-review", "review_id", rec.ID)
			for _, login := range uniqueRecipients(args.SenderLogin, args.PRAuthorLogin) {
				PushNotification(ctx, d.Cache, login, Notification{
					ID:       newNotifID(),
					ReviewID: rec.ID,
					Owner:    pr.Owner, Repo: pr.Repo, PR: pr.Number, Title: args.Title,
					Source: "webhook",
				})
			}
			return
		}
	}

	// 同步索引（同 manual review 路径）
	if d.Indexer != nil {
		indexPRChunks(ctx, d.Indexer, pr)
	}

	builder := d.Builder
	if builder == nil {
		builder = prctx.NewLayeredBuilder()
	}
	pCtx, err := builder.Build(ctx, pr)
	if err != nil {
		slog.Error("webhook: build prctx failed", "err", err)
		return
	}
	budget := toBudgetPayload(pCtx.BudgetReport)

	ctxByStage := buildPerStageContexts(ctx, builder, pr, pCtx)
	merged := mergeStages(ctx, ctxByStage, d.Provider, d.Models, d.StageModels)

	var (
		summaryBuf      strings.Builder
		risksData       json.RawMessage
		suggestionsData json.RawMessage
		errSeen         bool
	)
	for ev := range merged {
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
			errSeen = true
		}
	}
	if errSeen || risksData == nil || suggestionsData == nil {
		slog.Warn("webhook: review missing stage data; skip persist + bot review")
		return
	}

	var reviewID string
	if d.Store != nil {
		// webhook 创建的 review owner = PR 作者（无登录上下文，按 PR meta 归属）
		// 这样 PR 作者登录 lgtm.com 时能看到 + 删除自己仓库的自动评审
		reviewID = persistReview(d.Store, pr, summaryBuf.String(), risksData, suggestionsData, budget, "webhook", args.PRAuthorLogin)
	}

	// Push bot review 回 PR（用 installation token）
	if reviewID != "" && d.OAuthClient != nil && d.OAuthClient.AppID != 0 && len(d.OAuthClient.PrivateKeyPEM) > 0 {
		if err := postBotReview(ctx, d.OAuthClient, args.InstallationID, pr, reviewID, summaryBuf.String(), suggestionsData, args.TriggerAction); err != nil {
			slog.Warn("webhook: post bot review failed", "err", err)
		}
	} else {
		slog.Info("webhook: skip bot review (App ID / private key not configured)")
	}

	// Push 通知给 sender + PR author（dedupe 同人）
	// 一条 PR 评审能触发 toast 的人 = 触发事件的人 ∪ PR 作者
	// 这样 PR 作者无论谁触发都看得到（同事 push 重评 / chat bot /lgtm 也通知 ta）
	if reviewID != "" {
		recipients := uniqueRecipients(args.SenderLogin, args.PRAuthorLogin)
		for _, login := range recipients {
			PushNotification(ctx, d.Cache, login, Notification{
				ID:       newNotifID(),
				ReviewID: reviewID,
				Owner:    pr.Owner, Repo: pr.Repo, PR: pr.Number, Title: args.Title,
				Source: "webhook",
			})
		}
	}
	slog.Info("webhook: review pipeline done",
		"owner", pr.Owner, "repo", pr.Repo, "pr", pr.Number, "review_id", reviewID)
}

// postBotReview 用 installation token 以 App bot 身份发完整 review
// summary 在 body + lgtm.com 链接；每条 suggestion 作 inline comment（含 ```suggestion 块）
//
// 整段流程：
//  1. AppJWT 签名
//  2. 换 installation token
//  3. 解 suggestions JSON → 跳过缺 file/line 的（fork 文件名映射 issue）
//  4. PostPRReview 一次性发完整 review
func postBotReview(
	ctx context.Context,
	c *oauth.Client,
	installationID int64,
	pr gh.PullRequest,
	reviewID, summary string,
	suggRaw json.RawMessage,
	triggerAction string,
) error {
	if installationID == 0 {
		return fmt.Errorf("installation ID missing in webhook payload")
	}
	jwt, err := oauth.AppJWT(c.AppID, c.PrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("app jwt: %w", err)
	}
	tok, err := c.GetInstallationToken(ctx, jwt, installationID)
	if err != nil {
		return fmt.Errorf("installation token: %w", err)
	}

	var suggestions []review.Suggestion
	if len(suggRaw) > 0 {
		if err := json.Unmarshal(suggRaw, &suggestions); err != nil {
			return fmt.Errorf("parse suggestions: %w", err)
		}
	}

	inline := make([]oauth.ReviewCommentInline, 0, len(suggestions))
	for _, s := range suggestions {
		if s.File == "" || s.Line <= 0 {
			continue
		}
		inline = append(inline, oauth.ReviewCommentInline{
			Path: s.File,
			Line: s.Line,
			Side: "RIGHT",
			Body: buildSuggestionCommentBody(s),
		})
	}

	body := buildBotReviewBody(summary, reviewID, len(suggestions), triggerAction)
	_, err = c.PostPRReview(ctx, tok.Token, pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, body, inline)
	return err
}

// buildBotReviewBody 摘要 + 评审统计 + 跳 lgtm.com 链接
// trigger 区分文案：synchronize 时强调"已重评最新 push"；slash 时致谢 user 触发
func buildBotReviewBody(summary, reviewID string, sgCount int, trigger string) string {
	var sb strings.Builder
	switch trigger {
	case "synchronize":
		sb.WriteString("## 🔄 LGTM AI 重评（基于最新 push）\n\n")
	case "reopened":
		sb.WriteString("## 🔁 LGTM AI 重评（PR 重开）\n\n")
	case "slash_review":
		sb.WriteString("## 🤖 LGTM AI 评审（应 `/lgtm review` 触发）\n\n")
	default:
		sb.WriteString("## 🤖 LGTM AI 自动评审\n\n")
	}
	if summary != "" {
		sb.WriteString(summary)
		sb.WriteString("\n\n")
	}
	fmt.Fprintf(&sb, "✨ 共生成 **%d** 条修改建议，已附在 inline 评论里。点 GitHub 自带的「Apply suggestion」可一键 commit。\n\n", sgCount)
	if reviewID != "" {
		fmt.Fprintf(&sb, "🔗 完整评审（含风险列表 + RAG 召回上下文）：https://lgtm-alpha.vercel.app/review/%s\n", reviewID)
	}
	sb.WriteString("\n<sub>由 LGTM 自动生成。push 新 commit 会自动重评；评论 `/lgtm review` 手动触发；`/lgtm help` 看更多命令</sub>")
	return sb.String()
}

// verifyHMAC GitHub 签名格式 "sha256=<hex>"；timing-safe 比较
func verifyHMAC(sig string, body, secret []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return false
	}
	want, err := hex.DecodeString(strings.TrimPrefix(sig, prefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	got := mac.Sum(nil)
	return hmac.Equal(want, got)
}

func safePrefix(s string) string {
	if len(s) > 16 {
		return s[:16] + "..."
	}
	return s
}

// newNotifID 短 ulid-like id；ms 时间戳 + 随机后缀
// 不用 ulid 库避免新增依赖；用 time.Now().UnixNano() + 简短 random
func newNotifID() string {
	now := time.Now().UnixNano()
	return fmt.Sprintf("%d-%d", now, now%1_000_000)
}
