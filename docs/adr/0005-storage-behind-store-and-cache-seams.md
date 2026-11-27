# 0005. Storage behind Store and Cache interfaces; the RAG index stays on SQLite

- Status: Accepted
- Date: 2026-05-30 (documentation corrected 2026-08-17)
- Recorded: 2026-09-11 (retroactive)

## Context

The first version was built to run locally with no services, with deployment planned for later. Stored reviews and short-lived state (sessions, counters) have different needs.

[#55] introduced the `Cache` interface as groundwork for switching to Redis once deployed. [#65] added Postgres behind the existing `Store` interface, without an ORM, falling back to SQLite with no change in behaviour when `POSTGRES_URL` is empty. [#66] added Redis behind `Cache`.

For retrieval, [#77] rejected the sqlite-vss extension: building it into Docker and across platforms was expected to cost more than 30 minutes of debugging, and the expected scale was under 10,000 chunks per repository, where brute-force cosine similarity is fast enough. Keeping the index in its own SQLite file meant retrieval kept working after reviews moved to Postgres, without pgvector.

## Decision

- Stored reviews go through `store.Store`: SQLite by default, Postgres when `POSTGRES_URL` is set.
- Login sessions, agent memory, notifications and rate-limit counters go through `store.Cache`: in memory by default, Redis when `REDIS_URL` is set.
- The production deployment on Fly runs Postgres and Redis.
- The RAG index is a separate SQLite file (`RAG_DB_PATH`) searched by brute-force cosine similarity, whatever the review store is.

## Alternatives considered

- **sqlite-vss:** rejected in [#77] for the build cost above.
- **sqlite-vec, pgvector, Qdrant:** named in `docs/EXTENSIONS.md` and the README as upgrades for an index that outgrows brute force; not adopted.

## Consequences

- If Postgres or Redis cannot be reached at startup, the server logs the error and falls back to SQLite or memory. Only the startup log line `store ready type=postgres` shows which store is live ([#129]).
- The RAG index lives on a Fly volume, which pins the deployment to one machine ([#129]).
- The Postgres and Redis tests run only when `PG_TEST_URL` and `REDIS_TEST_URL` are set. CI sets neither, so the adapters production uses are not exercised in CI.
- `index.Indexer` can only upsert, so deleting a review leaves its chunks in the index.

## Sources

- [#55] `Cache` interface with an in-memory implementation
- [#65] Postgres store
- [#66] Redis cache
- [#77] SQLite brute-force cosine retriever, scoped per repository
- [#129] documentation corrected to the storage production actually runs

[#55]: https://github.com/ecstasoy/LGTM/pull/55
[#65]: https://github.com/ecstasoy/LGTM/pull/65
[#66]: https://github.com/ecstasoy/LGTM/pull/66
[#77]: https://github.com/ecstasoy/LGTM/pull/77
[#129]: https://github.com/ecstasoy/LGTM/pull/129
