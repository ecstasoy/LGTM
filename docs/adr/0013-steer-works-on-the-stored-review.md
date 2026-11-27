# 0013. Steer works on the stored review and never rewrites it

- Status: Accepted
- Date: 2026-05-30 (agent memory 2026-05-31)
- Recorded: 2026-09-11 (retroactive)

## Context

After reading a review, users want to push it in a direction ("look harder at concurrency") or ask questions about the pull request. [#50] added steering by re-running a stage with the user's text. It reuses the files stored with the review instead of fetching GitHub again, which saves API quota at the price of losing the conventions layer.

[#73] added an agent mode: a tool-calling agent over the pull request's files that answers in prose, deliberately not parsed back into risks or suggestions. [#101] gave the agent retrieval, and [#103] gave it memory of earlier follow-ups.

## Decision

- Steer (`POST /api/review/:id/steer`) runs over a stored review and never fetches GitHub.
- **Stage re-run:** re-runs risks or suggestions with the user's text added to the PR context and used as the retrieval query. The result is streamed to the browser and not written back to the review.
- **Agent run:** runs the agent with tools confined to the pull request's files, plus retrieval within the repository's scope. The answer is a message.
- **Agent memory:** for each agent turn, the user's text and the reply are kept per review, without tool calls or results: the last 10 turns, for 7 days. Leaving out tool results cuts memory use five- to tenfold ([#103]). Concurrent appends are last-write-wins ([#103]).

## Alternatives considered

- **Fetching the pull request again for each steer:** rejected in [#50] to save GitHub quota.
- **Parsing agent answers into risks and suggestions:** rejected in [#73].

## Consequences

- Why stage re-runs are not stored was never written down. *Inferred:* it keeps the original review, which may be shared or replayed from the cache, unchanged.
- Reloading the page shows the original risks and suggestions.
- Adopting addresses a suggestion by its position in the stored review, while the page shows the re-run list, so after a suggestions re-run the two can differ.
- Steer uses the deployment's stage models, not the original review's ([ADR-0008]), and has no conventions layer ([ADR-0010]).
- Agent memory belongs to the review, not the user, so everyone who steers an anonymous review shares it ([ADR-0003]).

## Sources

- [#50] steer endpoint re-running risks or suggestions with user guidance
- [#73] agent mode with tool-call frames
- [#101] `search_repo` tool with retrieval
- [#103] per-review agent memory with a sliding window

[#50]: https://github.com/ecstasoy/LGTM/pull/50
[#73]: https://github.com/ecstasoy/LGTM/pull/73
[#101]: https://github.com/ecstasoy/LGTM/pull/101
[#103]: https://github.com/ecstasoy/LGTM/pull/103
[ADR-0003]: 0003-owned-reviews-are-visible-only-to-their-owner.md
[ADR-0008]: 0008-per-stage-model-routing.md
[ADR-0010]: 0010-layered-pr-context-under-a-token-budget.md
