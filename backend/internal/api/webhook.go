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

// WebhookPR is the subset of GitHub pull_request webhook payload fields we use
// Only what is needed to trigger a review; base/head ref and other large blocks are ignored
type WebhookPR struct {
	Action      string `json:"action"`
	Number      int    `json:"number"`
	PullRequest struct {
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		HeadSHA string `json:"-"` // filled in by the backend during Fetch
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

// WebhookIssueComment is the issue_comment event payload; on GitHub a PR is also an Issue
// a non-empty issue.pull_request field → the comment was made on a PR
type WebhookIssueComment struct {
	Action string `json:"action"`
	Issue  struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		Title   string `json:"title"`
		User    struct {
			Login string `json:"login"` // PR author; also notified when a slash command triggers the review
		} `json:"user"`
		PullRequest *struct {
			URL     string `json:"url"`      // API URL
			HTMLURL string `json:"html_url"` // user-facing URL
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

// allowedPRActions is the allowlist of PR event actions that trigger an automatic review
// opened: new PR; synchronize: new commit pushed (head_sha changed); reopened: reopened
// any other action (closed / merged / labeled / ...) does not trigger
var allowedPRActions = map[string]bool{
	"opened":      true,
	"synchronize": true,
	"reopened":    true,
}

// WebhookGitHub POST /api/webhook/github
//
// Security: HMAC-SHA256 verification of X-Hub-Signature-256 (using GITHUB_APP_WEBHOOK_SECRET)
// Event routing: only pull_request.opened triggers; everything else gets 204
// Async: respond 200 immediately; the review runs in a goroutine (10-30s, past GitHub's 10s retry threshold)
//
// Idempotent: an existing review for the same (owner, repo, pr, head_sha) is skipped; only the notification is pushed
func WebhookGitHub(d Deps, webhookSecret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "read body: " + err.Error()})
			return
		}

		// HMAC check: sha256=<hex>
		sig := c.GetHeader("X-Hub-Signature-256")
		if webhookSecret == "" {
			slog.Warn("webhook: WEBHOOK_SECRET not configured; rejecting to prevent forged deliveries")
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

// handlePullRequestEvent triggers a review for opened / synchronize / reopened
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
		// notify the PR author too (on synchronize the sender is the pusher, who may not be the author)
		// PushNotification dedupes internally when they are the same person (it does not, in fact, so the caller handles it)
		PRAuthorLogin: p.PullRequest.User.Login,
		TriggerAction: p.Action, // used in the bot review body (distinguishes a fresh review from a re-review)
	})
}

// handleIssueCommentEvent parses /lgtm <cmd> slash commands in PR comments
// Triggers on: action=created + the issue is a PR + the first line of the body is /lgtm review
// Guards against the bot's own replies looping: a sender.login ending in [bot] is ignored outright
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
	// PR comments only — it is a PR precisely when issue.pull_request is non-empty
	if p.Issue.PullRequest == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ignored": "non-PR issue comment"})
		return
	}
	// loop guard: the bot's own reply is also an issue_comment and would come back as another webhook
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
		// ack unrecognized commands too, listing what is available
		go runSlashHelp(d, p.Repository.Owner.Login, p.Repository.Name, p.Issue.Number, p.Installation.ID)
	}
}

// parseSlashCommand finds the first /lgtm <cmd> line in body and returns <cmd>; "" when there is none
// Commands are case-insensitive; leading whitespace is ignored
func parseSlashCommand(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "/lgtm") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "/lgtm"))
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return "review" // a bare /lgtm defaults to review
		}
		return strings.ToLower(fields[0])
	}
	return ""
}

// runSlashReview is the re-review triggered by a slash command; it posts an ack comment first, then runs the review
func runSlashReview(d Deps, args slashReviewArgs) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// the ack comment uses the installation token, so the user sees "LGTM got it" immediately
	if d.OAuthClient != nil && d.OAuthClient.AppID != 0 && len(d.OAuthClient.PrivateKeyPEM) > 0 && args.InstallationID != 0 {
		jwt, err := oauth.AppJWT(d.OAuthClient.AppID, d.OAuthClient.PrivateKeyPEM)
		if err != nil {
			slog.Warn("slash review: app jwt failed", "err", err)
		} else if tok, err := d.OAuthClient.GetInstallationToken(ctx, jwt, args.InstallationID); err != nil {
			slog.Warn("slash review: installation token failed", "err", err)
		} else {
			_, _ = d.OAuthClient.PostIssueComment(ctx, tok.Token, args.Owner, args.Repo, args.Number,
				"🤖 LGTM got your `/lgtm review` — reviewing now (full review lands in ~30s)…")
		}
	}

	// reuses the whole runWebhookReview path: fetch → idempotency check → index → review → post bot review
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

// runSlashHelp lists the available commands when one is not recognized
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
	body := "🤖 **LGTM available commands**\n\n" +
		"- `/lgtm review` — re-review the current PR (same logic that runs automatically on a new push)\n" +
		"- `/lgtm help` — show this help\n\n" +
		"<sub>More commands are in the works (e.g. `/lgtm explain <file>:<line>` to explain a single line)</sub>"
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

// uniqueRecipients dedupes notification recipients: empty strings are dropped, duplicates collapse to one
// Covers the common case of PR author == sender (they match on opened)
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
	// SenderLogin is the GitHub user who triggered this event (whoever pushed / commented /lgtm)
	SenderLogin string
	// PRAuthorLogin is the PR author; usually the same as SenderLogin (they match on opened),
	// but on synchronize the sender is the pusher, who may not be the author, and on a slash command the sender is the commenter, who also may not be
	// both are notified (the caller dedupes) so the PR author always hears about it
	PRAuthorLogin string
	// TriggerAction "opened" / "synchronize" / "reopened" / "slash_review"；
	// used to vary the wording in the bot review body ("re-reviewed the latest pushed commit" vs "first review")
	TriggerAction string
}

// runWebhookReview runs the review in the background, then persists, pushes a notification and posts the bot review
// A failure at any step is logged but not retried (demo simplification; production would add a queue + retry)
func runWebhookReview(d Deps, args webhookReviewArgs) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	pr, err := d.Fetcher.Fetch(ctx, args.PrURL)
	if err != nil {
		slog.Error("webhook: fetch PR failed", "err", err, "url", args.PrURL)
		return
	}

	// webhook has no user request to negotiate a locale from; it always uses the deployment-configured default
	// (already normalized to "zh"/"en" at startup — see Deps.DefaultLocale / effectiveDefaultLocale).
	locale := effectiveDefaultLocale(d)

	// idempotency check: an existing review for the same (owner, repo, pr, head_sha, locale) → skip
	if d.Store != nil {
		if rec, _ := d.Store.Get(ctx, pr.Owner, pr.Repo, pr.Number, pr.HeadSHA, string(locale)); rec != nil {
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

	// index inline (same as the manual review path)
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
	merged := mergeStages(ctx, ctxByStage, d.Provider, d.Models, d.StageModels, locale)

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
		// a webhook-created review is owned by the PR author (there is no login context, so ownership follows PR meta)
		// that way the PR author sees and can delete their own repo's automatic reviews once logged in to lgtm.com
		reviewID = persistReview(d.Store, pr, summaryBuf.String(), risksData, suggestionsData, budget, "webhook", args.PRAuthorLogin, string(locale))
	}

	// push the bot review back to the PR (using the installation token)
	if reviewID != "" && d.OAuthClient != nil && d.OAuthClient.AppID != 0 && len(d.OAuthClient.PrivateKeyPEM) > 0 {
		if err := postBotReview(ctx, d.OAuthClient, args.InstallationID, pr, reviewID, summaryBuf.String(), suggestionsData, args.TriggerAction); err != nil {
			slog.Warn("webhook: post bot review failed", "err", err)
		}
	} else {
		slog.Info("webhook: skip bot review (App ID / private key not configured)")
	}

	// notify the sender + the PR author (same person is deduped)
	// the people a PR review can toast = whoever triggered the event ∪ the PR author
	// so the PR author always sees it no matter who triggered it (a colleague pushing a re-review, a chat bot running /lgtm)
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

// postBotReview posts the full review as the App bot, using the installation token
// The summary goes in the body along with an lgtm.com link; each suggestion becomes an inline comment (with a ```suggestion block)
//
// The whole flow:
// 1. sign the AppJWT
// 2. exchange it for an installation token
// 3. parse the suggestions JSON → skip any missing file/line (fork filename mapping issue)
// 4. PostPRReview sends the complete review in one call
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

// buildBotReviewBody assembles the summary + review stats + a link out to lgtm.com
// trigger varies the wording: synchronize emphasizes "re-reviewed the latest push", a slash command thanks the user who triggered it
//
// Everything this function returns is posted straight to GitHub, so it is hardcoded English regardless of
// DEFAULT_LOCALE — see the package-level note in comment.go for the rule this follows.
func buildBotReviewBody(summary, reviewID string, sgCount int, trigger string) string {
	var sb strings.Builder
	switch trigger {
	case "synchronize":
		sb.WriteString("## 🔄 LGTM AI re-review (latest push)\n\n")
	case "reopened":
		sb.WriteString("## 🔁 LGTM AI re-review (PR reopened)\n\n")
	case "slash_review":
		sb.WriteString("## 🤖 LGTM AI review (triggered by `/lgtm review`)\n\n")
	default:
		sb.WriteString("## 🤖 LGTM AI automatic review\n\n")
	}
	if summary != "" {
		sb.WriteString(summary)
		sb.WriteString("\n\n")
	}
	switch sgCount {
	case 0:
		sb.WriteString("✨ No inline suggestions this time — nothing stood out at the line level.\n\n")
	case 1:
		sb.WriteString("✨ Generated **1** suggestion, attached as an inline comment. Use GitHub's built-in \"Apply suggestion\" to commit it with a click.\n\n")
	default:
		fmt.Fprintf(&sb, "✨ Generated **%d** suggestions, attached as inline comments. Use GitHub's built-in \"Apply suggestion\" to commit any of them with a click.\n\n", sgCount)
	}
	if reviewID != "" {
		fmt.Fprintf(&sb, "🔗 Full review (risk list + RAG-retrieved context): https://lgtm-alpha.vercel.app/review/%s\n", reviewID)
	}
	sb.WriteString("\n<sub>Posted automatically by LGTM. Pushing a new commit triggers a re-review; comment `/lgtm review` to trigger one manually; `/lgtm help` for more commands</sub>")
	return sb.String()
}

// verifyHMAC checks GitHub's "sha256=<hex>" signature format with a timing-safe comparison
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

// newNotifID is a short ulid-like id: a millisecond timestamp + a random suffix
// Avoids a ulid dependency; uses time.Now().UnixNano() + a short random instead
func newNotifID() string {
	now := time.Now().UnixNano()
	return fmt.Sprintf("%d-%d", now, now%1_000_000)
}
