# LGTM domain context

The shared vocabulary for LGTM, an AI pull request review assistant. Use these terms in code, API fields, UI copy, commit messages and design discussions. When the codebase uses a word differently from this file, treat the code as the thing to rename.

- One term, one meaning. **Avoid** names words that mean something else here.
- Code anchors name types and functions, not line numbers.
- UI labels are given as English / Chinese where the product shows the term.
- Decisions and their reasons live in `docs/adr/`, not here.

## Pull requests and reviews

**Pull request (PR)**
A GitHub pull request, identified by owner, repo and number, fetched as a snapshot at its head SHA: metadata, changed files with their diffs, conventions and CI status.
Code: `github.PullRequest`, `github.ParseURL`, `github.Fetcher`.

**Head SHA**
The commit a pull request pointed at when it was fetched. A new push means a new head SHA, and so a new review.

**Conventions**
The repository's own guidance, read at the head SHA: `README.md`, `CONTRIBUTING.md`, and the first of `CLAUDE.md` or `AGENTS.md`. Capped at 16 KB.
Code: `github.Conventions`.

**Review**
The stored result of reviewing one pull request at one head SHA in one locale: a summary, risks, suggestions and a budget report. Identified by a ULID review id. A review exists only if its review run completed every stage.
Code: `store.Record` (row), `api.cachedPayload` (stored content).
UI: "Review" / "评审".
Avoid: `ReviewSummary` in `frontend/lib/types.ts` is a history list row, not the summary stage.

**Review run**
One execution of the review pipeline: fetch the pull request, build PR context, run the three stages in parallel, stream stage events, and store the review if every stage completed. A run either replays a review from the review cache or produces a new one.
Code: `api.PostReview` (web), `api.runWebhookReview` (webhook). `review.Orchestrator` is not wired in.

**Review source**
How a review run started: `manual` (someone submitted a PR URL on the web) or `webhook` (the GitHub App reacted to a pull request event or a slash command).
Code: `cachedPayload.Source`.
UI: webhook reviews carry "⚡ Auto" / "⚡ 自动".

**Review owner**
The GitHub login a review belongs to: the signed-in user who submitted it or, for a webhook review, the pull request's author.
Code: `store.Record.UserID` (a login string, despite the name).
Avoid: "owner" alone. `store.Record.Owner` is the repository owner.

**Owned review / anonymous review**
An owned review has a review owner; only that owner can view, steer or delete it. An anonymous review has no owner: it was submitted without signing in, anyone with its id can view and steer it, any signed-in user can delete it, and it appears in nobody's history.
Code: `api.canViewReview`, `api.DeleteReview`.
Avoid: "legacy record" for anonymous reviews; parts of the UI still use it, but anonymous reviewing is a supported path.

**Stage**
One independent model pass over the PR context. There are three, run in parallel: **summary** (streamed Markdown), **risks** and **suggestions** (each a JSON list emitted once).
Code: `review.Stage`, `review.SummaryStage`, `review.RisksStage`, `review.SuggestionsStage`.
UI: "Summary / Risks / Suggestions" / "摘要 / 风险 / 建议".

**Risk**
A problem the risks stage found: file, optional line, severity (`high`, `medium`, `low`), category (`bug`, `security`, `perf`, `style`, `concurrency`, `breaking`, `other`), confidence from 0 to 1, and a reason. A risk never carries a code change.
Code: `review.Risk`.

**Suggestion**
A proposed improvement from the suggestions stage: file, line, type, title, body, and optionally a code change (before and after) that can be adopted. A suggestion has no id; it is addressed by its position in the stored review.
Code: `review.Suggestion`, `review.Patch`.
Avoid: calling the code change a "patch" unqualified; a patch is a file's diff.

**Stage event**
A message from a running stage: summary text, the finished risks, the finished suggestions, an error, or done. Stage events and run-level frames (PR, files, budget report, review id) reach the browser as server-sent events, and every stream ends with exactly one final `done`.
Code: `review.Event`, `frontend/lib/sse.ts`.

**Review cache**
Reusing a stored review instead of running the stages again. A run replays a review that already exists for the same owner, repo, PR number, head SHA and locale. Only anonymous reviews are matched, and a run with a non-default model skips the cache.
Code: `store.Store.Get`, `api.replayCached`.
Avoid: "cache" for `store.Cache`, the key-value store behind login sessions, agent memory, notifications and rate-limit counters.

**Locale**
The language a review is written in: `zh` or `en`. Resolved from the request body, then `Accept-Language`, then the deployment default; webhook runs always use the deployment default. Locale is part of a review's identity. Text posted to GitHub is English whatever the locale.
Code: `i18n.Locale`, `api.resolveLocale`.
Avoid: `lang`, which is the pull request's main programming language (`api.detectPrimaryLang`).

## Models

**Model profile**
A named model the deployment offers: key, display label, provider endpoint and model name.
Code: entries of `llm.Registry`, configured with `LLM_MODELS`.

**Model registry**
The deployment's model profiles; the first is the default. `GET /api/models` publishes it as the allowlist users choose from.
Code: `llm.Registry`.
UI: "Choose review model" / "选择评审模型".

**Stage models**
The deployment's model per stage (`SUMMARY_MODEL`, `RISKS_MODEL`, `SUGGESTIONS_MODEL`). A web request may override them with one model for every stage or one per stage, chosen from the model registry.
Code: `api.Deps.StageModels`; request fields `model` and `stage_models`.
UI: "Choose model per stage" / "分阶段选择模型".

## PR context

**PR context**
What a stage's prompt is built from: four context layers assembled per stage under a token budget.
Code: `prctx.Context`, `prctx.LayeredBuilder`.

**Context layers (L1–L4)**
- **L1 metadata**: repo, number, title, description and the list of changed files. Always kept.
- **L2 changed files**: each file's diff, added in PR order while budget remains. A file that does not fit is dropped whole.
- **L3 conventions**: the repository's conventions, up to 10% of the budget.
- **L4 references**: RAG references, up to 20% of the budget, only when retrieval is enabled.

Avoid: "L1/L2/L3" for model routing tiers, which some code comments do.

**Token budget**
The size a PR context must fit: 48,000 tokens by default, estimated as characters ÷ 3.
Code: `prctx.DefaultTokenLimit`.

**Budget report**
How much of the token budget each layer used and which files were dropped. Streamed to the browser and stored with the review.
Code: `prctx.BudgetReport`.

**Dropped file**
A changed file whose diff did not fit the token budget. Every stage is told which files were dropped and to reach no verdict on them, and the UI says how many were left out, so a review never implies it covered code it did not see.
UI: "Dropped — over budget" / "超预算丢弃".

## Retrieval (RAG)

**RAG index**
Embedded chunks of repository code, searched by similarity to bring related code from outside the diff into a review. Always a SQLite file, separate from where reviews are stored.
Code: `index.Indexer`, `index.Retriever`, `index.SQLiteRetriever`.

**Scope**
The repository a chunk belongs to, `owner/repo`, compared case-insensitively. Retrieval never crosses scopes.

**Chunk**
One indexed piece of code, keyed by scope, path and position. Two kinds exist:
- **PR chunk**: one diff hunk of a pull request, indexed when a review run starts (not on replays).
- **Snapshot chunk**: an overlapping window of a file from a full repository checkout, indexed offline by `cmd/indexrepo`.

Both kinds share one key space, so one can overwrite the other.

**Reference**
A chunk returned by retrieval, with its similarity score. The L4 layer keeps references scoring at least 0.35 whose files are not already in L2, up to four.
Code: `index.Reference`.

## Steering and the agent

**Steer**
A follow-up instruction on a stored review, run without fetching GitHub again. It is either a stage re-run or an agent run. Nothing steer produces is written back to the review.
Code: `api.PostSteer` (`POST /api/review/:id/steer`, field `mode`).
UI: "User steering" / "用户引导".
Avoid: "steer the agent" for a stage re-run; only an agent run involves the agent.

**Stage re-run**
The steer mode that runs the risks or suggestions stage again, with the user's text added to the PR context and used as the retrieval query. The page replaces its list with the result; a reload brings back the stored list.
UI: "Re-review risks" / "重评风险", "Regenerate suggestions" / "重出建议".

**Agent run**
The steer mode that answers the user with the agent. The follow-up panel always uses it.
UI: "Follow-up" / "追问", "Agent deep dive" / "Agent 深挖".

**Agent**
A loop that calls the model, runs the tools the model asks for, feeds the results back, and repeats until the model answers or runs out of steps. A step is one model call; steer allows 8. Repeating an identical tool call (same tool, same arguments) reuses the earlier result once with a warning, and a second repeat stops the run.
Code: `backend/internal/agent/agent.go`.

**Tool / tool call**
A capability the agent may use, and one use of it. Tools are confined to the pull request's changed files (`read_file`, `list_dir`, `grep_patches`), plus `search_repo` for retrieval within the repository's scope when retrieval is enabled. Each call streams as `tool_call_start` and `tool_call_done`.
Code: `backend/internal/agent/builtin.go`.
UI: "Call {name}" / "调用 {name}".

**Agent memory**
Earlier agent runs on the same review, given to the next agent run as context: the user's text and the agent's reply for the last 10 turns, kept for 7 days. Tool calls and results are not kept. Agent memory belongs to the review, not to a user.
Code: `memory.SessionStore`, `memory.CacheSessionStore`.
Avoid: "session" or "session memory"; a session is a login session.

## Acting on GitHub

**Login session**
A signed-in GitHub user's server-side state: GitHub id, login, profile and GitHub token. The token never reaches the browser. Identified by an HTTP-only cookie and kept for 30 days.
Code: `session.Session`, `session.Manager`.
UI: "Sign in with GitHub" / "GitHub 登录".
Avoid: "session" for agent memory or for the review page's "Session" view.

**GitHub App**
LGTM's app on GitHub. It signs users in, receives webhook events, and posts bot reviews.
Code: `oauth.Client`.
UI: "Install the LGTM App" / "装 LGTM App".

**Installation / installation token**
The GitHub App installed on an account or organisation, and the one-hour token that lets LGTM act as the bot there.

**Webhook review**
A review run started by GitHub: a pull request opened, synchronized or reopened, or a slash command. It runs in the background and is owned by the pull request's author.
Code: `api.WebhookGitHub`, `api.runWebhookReview`.

**Slash command**
A pull request comment line starting with `/lgtm`. `/lgtm` or `/lgtm review` starts a webhook review; `/lgtm help`, or anything unrecognised, gets usage help. The bot acknowledges each command with a comment.

**Bot review**
The pull request review LGTM posts on GitHub after a webhook review: the summary as its body and suggestions as inline suggestion comments. Always a comment, never an approval.
Code: `backend/internal/oauth/review_full.go`.
Avoid: "review" alone, which means LGTM's stored review.

**Adopt**
Taking a suggestion onto the pull request as the signed-in user, in one of two ways:
- **Comment**: post the suggestion as a review comment on its file and line. Needs triage permission or higher on the base repository. Undo deletes the comment.
- **Commit**: post the comment, then apply it as a commit on the pull request's branch. Needs write permission, and GitHub may still refuse, for example on a fork that disallows maintainer edits.

Code: `api.PostAdoptComment`, `api.PostAdoptCommit`, `api.DeleteAdoptComment`, `backend/internal/oauth/apply.go`.
UI: "Comment on PR" / "评论到 PR", "Commit directly" / "直接提交", "Undo" / "撤回".

**Adoption count**
How many of a review's suggestions were adopted. Tracked only in the browser.
UI: "n/total adopted" / "采纳 n/total".

**Repo permission**
What the signed-in user may do on the base repository, reported as `can_comment` and `can_commit`, with a reason code when not allowed.
Code: `GET /api/perms`, `backend/internal/oauth/perms.go`.

**Notification**
An in-app message that a webhook review finished, sent to whoever triggered it and to the pull request's author. The newest 50 per user are kept for 7 days and shown as a toast, but only if they arrive while the page is open.
Code: `api.PushNotification`, `frontend/lib/notifications.ts`.
UI: "PR reviewed automatically" / "PR 已自动评审".

## Platform

**Error code**
A stable, machine-readable `code` on API errors and on stream `error` and `info` frames. The frontend maps known codes to wording in each language.
Code: `backend/internal/api/errcode.go`, `friendlyError` in `frontend/lib/errors.ts`.

**Rate limit**
Requests counted per client IP in fixed windows: 5 per 25 seconds on expensive endpoints (review, steer, adopt) and 20 per 4 seconds on reads. Over the limit a request gets 429 with `Retry-After` and the code `rate_limited`. If counting fails, requests go through.
Code: `middleware.RateLimit`.

**Client IP**
The address rate limits count against: the header named by `TRUSTED_PLATFORM` (for example `Fly-Client-IP`) when set, otherwise resolved through `TRUSTED_PROXIES`, otherwise the connecting address.

## Overloaded names

Words the codebase uses for more than one concept. In new code, copy and discussion, use the term on the right; rename old uses when touching them.

| Word | Meanings in the codebase today | Use instead |
|---|---|---|
| session | login session (`session.Session`); agent memory (`memory.SessionStore`); the review page's "Session" view and `SessionList` | login session · agent memory · session view |
| cache | review cache (`store.Store.Get` replay); the key-value store `store.Cache`; browser `localStorage` | review cache · `store.Cache` · browser storage |
| review | LGTM's stored review; one review run; the bot review posted on GitHub | review · review run · bot review |
| owner | repository owner (`store.Record.Owner`); review owner (`store.Record.UserID`, error `not_review_owner`) | repository owner · review owner |
| steer, follow-up | "steer" covers both modes; "follow-up" means only the agent run; stage re-run copy says "steer the agent" | stage re-run · agent run |
| L1, L2, L3 | context layers; model routing tiers in some PR titles and comments | context layers only |
| patch | a file's diff (`github.File.Patch`); a suggestion's code change (`review.Suggestion.Patch`) | diff · code change |
| lang, locale | the PR's main programming language (`lang`); the review's language (`locale`); a code change's language (`Patch.lang`) | programming language · locale |
| id in the `pr` frame | the head SHA, while every other `id` is a review id | head SHA |
| summary | the summary stage; `ReviewSummary`, a history list row; Chinese UI uses both "摘要" and "总结" for the stage | summary stage · review list item |
| user id | a GitHub login in `store.Record.UserID`; a numeric GitHub id in `session.Session.UserID` | login · GitHub user id |
| index, idx | the RAG index; a chunk's position; a suggestion's position; database indexes | RAG index · chunk position · suggestion position |
