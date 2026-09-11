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
