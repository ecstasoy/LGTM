# 0004. A review is identified by pull request, head SHA and locale

- Status: Accepted
- Date: 2026-05-29 (locale added 2026-08-13)
- Recorded: 2026-09-11 (retroactive)

## Context

A review run takes tens of seconds and costs model tokens, and the same pull request at the same head SHA gets asked for again: page reloads, shared links, repeated webhook deliveries. [#22] and [#23] made stored reviews serve as both history and a cache.

Two early choices protect links to a review. `INSERT OR REPLACE` would give an updated row a new id and break deep links, so writes update in place. Ids are ULIDs: time-ordered and generated from crypto randomness. The stored content is a JSON document, so older reviews stay readable when fields are added.

When English reviews arrived ([#123]), a key without locale would have handed a cached Chinese review to an English request, and overwritten the Chinese review with the English one. Locale is also the first part of the key a caller controls, so it is normalised before it reaches the store.

## Decision

- An anonymous review is unique per repository owner, repo, PR number, head SHA and locale. An owned review is unique per review owner plus those five.
- Writing a review that already exists keeps the existing id and replaces its content and timestamp.
- Only a complete run is stored: no stage failed, and both risks and suggestions arrived.
- A review run replays a stored review that matches the key instead of running the stages. This is the review cache.

## Alternatives considered

- **`INSERT OR REPLACE`:** rejected in [#22] because it changes the id.
- **Model in the key:** not done; runs with a non-default model skip the cache instead ([ADR-0008]).

## Consequences

- A new push means a new head SHA and a new review; nothing is invalidated explicitly.
- `store.Store.Get` matches only anonymous reviews. Signed-in reviews and webhook reviews, which are owned by the pull request's author, never replay from the cache, and the webhook's duplicate-delivery check does not see them ([ADR-0012]).
- Reviews stored before locale existed were backfilled as `zh`.

## Sources

- [#22] SQLite store with stable ids
- [#23] cache by head SHA with replay on hit
- [#123] reviews generated in the requested locale; locale added to the unique indexes

[#22]: https://github.com/ecstasoy/LGTM/pull/22
[#23]: https://github.com/ecstasoy/LGTM/pull/23
[#123]: https://github.com/ecstasoy/LGTM/pull/123
[ADR-0008]: 0008-per-stage-model-routing.md
[ADR-0012]: 0012-webhook-reviews-without-a-queue.md
