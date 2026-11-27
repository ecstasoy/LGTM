# 0003. Owned reviews are visible only to their owner; everyone else gets 404

- Status: Accepted
- Date: 2026-05-31 (extended to steer on 2026-09-11)
- Recorded: 2026-09-11 (retroactive)

## Context

Per-user history ([#95]) means a review can have a review owner. Review ids are ULIDs built from crypto randomness, so they cannot be guessed, but they do leak: `/review/[id]` URLs get shared and seen ([#132]).

Until [#132], steer checked nothing. A non-owner holding an id could steer an owned review and get the model to print diffs the detail endpoint refuses to show, write turns into the owner's agent memory, and spend model quota.

## Decision

- An anonymous review can be viewed and steered by anyone with its id.
- An owned review can be viewed and steered only by its review owner. Anyone else, signed in or not, gets **404, not 403**, so a leaked id does not confirm that someone else's review exists.
- One function, `api.canViewReview`, holds the rule. `GetReview` and `PostSteer` both call it before opening a stream, calling a model or touching agent memory.
- A webhook review is owned by the pull request's author, because a webhook has no signed-in user.
- Adopting a suggestion does not use this rule. It requires sign-in plus GitHub permission on the target repository, which is the resource being written to ([#132]).

## Alternatives considered

- **Require sign-in to steer:** rejected in [#132]. It would stop anonymous users steering their own reviews, and protect nothing `GetReview` does not already expose.

## Consequences

- Every endpoint that loads a review by id must either call `canViewReview` or state why it does not.
- `DeleteReview` answers a non-owner with 403 (`not_review_owner`), not 404, so deleting confirms existence where viewing does not.
- Because adopting relies on repository permission, a user with comment permission on a repository who holds an owned review's id can post that review's suggestions without being able to view it. *Inferred.*
- Agent memory is keyed by review, so everyone who steers the same anonymous review shares one conversation history.

## Sources

- [#95] per-user visibility for stored reviews
- [#132] steer requires review ownership; shared `canViewReview`

[#95]: https://github.com/ecstasoy/LGTM/pull/95
[#132]: https://github.com/ecstasoy/LGTM/pull/132
