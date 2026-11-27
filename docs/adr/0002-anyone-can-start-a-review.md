# 0002. Anyone can start a review; sign-in gates history, notifications and write-back

- Status: Accepted
- Date: 2026-05-31
- Recorded: 2026-09-11 (retroactive)

## Context

Every review run spends model tokens. [#95] required signing in with GitHub before `POST /api/review`, to stop anonymous visitors spending the LLM budget. The same day, [#102] removed that gate: making people sign in with GitHub before their first review was too much friction for anyone trying the product, and the rate limit on expensive endpoints plus the review cache were judged enough to contain abuse.

The review list is different. It shows pull request titles, repositories and risk content, so it stays behind sign-in ([#97]).

## Decision

- Starting a review needs no sign-in. A signed-in user's review becomes an owned review; a review submitted without signing in is an anonymous review.
- Sign-in is required for review history (`GET /api/reviews`), deleting a review, notifications, and adopting a suggestion.
- Anonymous reviews are stored like any other review, without a review owner.

## Alternatives considered

- **Sign-in before any review** ([#95]): shipped, then removed by [#102] for the friction above.
- **Deferred in [#102]:** a tighter per-IP limit for anonymous requests, and expiry or cleanup of anonymous reviews.

## Consequences

- The rate limit is the main guard between an anonymous visitor and the model budget ([ADR-0015]).
- Anonymous reviews appear in nobody's history, and any signed-in user may delete one (`api.DeleteReview`).
- Parts of the code and UI still call anonymous reviews "legacy", and a comment in `api.PostReview` says they skip the store. Both predate this decision and are wrong: anonymous reviews are stored.
- Pull requests are fetched with the deployment's GitHub token ([ADR-0014]), so anyone who can submit a URL can get a review of any repository that token can read. *Inferred; not discussed in [#102].*

## Sources

- [#95] login required to start a review; per-user visibility and delete
- [#97] review list behind sign-in
- [#102] anonymous reviews allowed; sign-in gates only history, notifications and write-back

[#95]: https://github.com/ecstasoy/LGTM/pull/95
[#97]: https://github.com/ecstasoy/LGTM/pull/97
[#102]: https://github.com/ecstasoy/LGTM/pull/102
[ADR-0014]: 0014-separate-github-identities-for-reading-and-writing.md
[ADR-0015]: 0015-rate-limit-on-the-platform-client-ip.md
