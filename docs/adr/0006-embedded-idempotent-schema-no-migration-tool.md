# 0006. Apply the schema at startup from embedded SQL, without a migration tool

- Status: Accepted
- Date: 2026-05-29
- Recorded: 2026-09-11 (retroactive)

## Context

The store has a single `reviews` table. [#22] judged migration tools such as goose or golang-migrate overkill for it, and [#65] kept the same approach when adding Postgres.

## Decision

- The schema and indexes are embedded in the binary as SQL and applied on every startup.
- Statements are idempotent, so a fresh or an up-to-date database both start cleanly.
- A change to an existing table is a hand-written step that detects whether it has already been applied and skips itself if so.

## Alternatives considered

- **goose or golang-migrate:** rejected as overkill for one table ([#22]).

## Consequences

- No migration tool or migration state table; a new database works on first start.
- There is no versioned history of schema changes. Adding locale to the unique indexes ([#123]) took a hand-written migration, run in one transaction on SQLite, while the Postgres path has no single transaction around it.
- Each new table, and each change to an existing one, makes this approach riskier. A backup and migration strategy for the deployed database was still open in [#129].

## Sources

- [#22] SQLite store with schema applied at startup
- [#65] Postgres store using the same approach
- [#123] hand-written migration adding locale to the unique indexes
- [#129] storage documentation, with backups and migrations listed as open

[#22]: https://github.com/ecstasoy/LGTM/pull/22
[#65]: https://github.com/ecstasoy/LGTM/pull/65
[#123]: https://github.com/ecstasoy/LGTM/pull/123
[#129]: https://github.com/ecstasoy/LGTM/pull/129
