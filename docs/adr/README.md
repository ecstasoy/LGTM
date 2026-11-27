# Architecture decision records

What LGTM decided, why, and what it costs. [ADR-0001](0001-record-architecture-decisions.md) explains when to write one and how decisions change over time. Domain terms follow `CONTEXT.md` at the repository root.

To record a decision, copy [`0000-template.md`](0000-template.md) to the next number and add it in the same pull request as the change.

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](0002-anyone-can-start-a-review.md) | Anyone can start a review; sign-in gates history, notifications and write-back | Accepted |
| [0003](0003-owned-reviews-are-visible-only-to-their-owner.md) | Owned reviews are visible only to their owner; everyone else gets 404 | Accepted |
| [0004](0004-review-identity-and-cache-key.md) | A review is identified by pull request, head SHA and locale | Accepted |
| [0005](0005-storage-behind-store-and-cache-seams.md) | Storage behind Store and Cache interfaces; the RAG index stays on SQLite | Accepted |
| [0006](0006-embedded-idempotent-schema-no-migration-tool.md) | Apply the schema at startup from embedded SQL, without a migration tool | Accepted |
| [0007](0007-openai-compatible-providers-only.md) | Talk to models through one OpenAI-compatible adapter, with a mock fallback | Accepted |
| [0008](0008-per-stage-model-routing.md) | Route each stage to its own model through a named model registry | Accepted |
| [0009](0009-review-locale-and-english-on-github.md) | Reviews are written in the requester's locale; GitHub gets English | Accepted |
| [0010](0010-layered-pr-context-under-a-token-budget.md) | Build PR context in layers under a fixed token budget, and say what was left out | Accepted |
| [0011](0011-stream-reviews-over-sse.md) | Stream review runs to the browser over server-sent events | Accepted |
| [0012](0012-webhook-reviews-without-a-queue.md) | Run webhook reviews in the background, without a queue | Accepted |
| [0013](0013-steer-works-on-the-stored-review.md) | Steer works on the stored review and never rewrites it | Accepted |
| [0014](0014-separate-github-identities-for-reading-and-writing.md) | Read pull requests with the deployment token; write back as the user or the App | Accepted |
| [0015](0015-rate-limit-on-the-platform-client-ip.md) | Rate-limit per client IP, taken from the platform's header | Accepted |
