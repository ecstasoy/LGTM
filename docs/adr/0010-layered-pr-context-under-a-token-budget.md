# 0010. Build PR context in layers under a fixed token budget, and say what was left out

- Status: Accepted
- Date: 2026-05-29 (retrieval layer 2026-05-30, review scope reporting 2026-06-14)
- Recorded: 2026-09-11 (retroactive)

## Context

A pull request can be bigger than a model's context window. [#19] set the default budget at 48,000 tokens: DeepSeek's 64K input minus a 16K reserve for output. A real tokenizer was judged overkill, so tokens are estimated as characters ÷ 3. Cutting a diff part-way breaks its hunk headers, so a file either fits whole or not at all.

[#78] added retrieved code as a fourth layer, and [#83] and [#86] tuned it: hunk-level chunks, a query per stage, and a similarity threshold lowered from 0.5 to 0.35 because related code in another language scores between 0.35 and 0.50.

Large pull requests used to fail quietly: files past the first page were never fetched, and files that did not fit vanished from the prompt without a trace ([#106]).

## Decision

- Each stage's PR context has four layers:
  - L1 metadata, always kept.
  - L2 changed files: whole diffs in PR order while budget remains.
  - L3 conventions: up to 10% of the budget.
  - L4 references: up to 20%, only when retrieval is enabled. At least 0.35 similarity, not already in L2, top four.
- All changed files are fetched, up to 30 pages of 100. L1 lists at most 300 files but states the true total.
- Dropped files are named in every stage prompt with an instruction to reach no verdict on them, listed in the budget report, and shown in the UI.

## Alternatives considered

- **tiktoken:** rejected in [#19] as overkill.
- **Truncating diffs:** rejected in [#19] because it corrupts hunks.
- **Syntax-aware (tree-sitter) chunking:** suggested in `docs/EXTENSIONS.md`; hunk chunking was chosen ([#83]).
- **Separate limits in the fetcher and in the budget:** rejected in [#106] in favour of one place that reports what was left out.

## Consequences

- A review states its own coverage. A large file can be left out of a review entirely, and the user is told.
- The budget does not follow the routed model's context window ([ADR-0008]).
- Per-stage retrieval adds a query per stage, about 200 ms ([#83]).
- Steer rebuilds context from the stored review and has no L3 ([ADR-0013]).

## Sources

- [#19] layered builder with a token budget
- [#78] retrieval wired in as L4
- [#83] similarity threshold, L2/L4 de-duplication, per-stage queries, hunk chunks
- [#86] threshold lowered to 0.35
- [#106] full file fetch and dropped files visible to the model and the user

[#19]: https://github.com/ecstasoy/LGTM/pull/19
[#78]: https://github.com/ecstasoy/LGTM/pull/78
[#83]: https://github.com/ecstasoy/LGTM/pull/83
[#86]: https://github.com/ecstasoy/LGTM/pull/86
[#106]: https://github.com/ecstasoy/LGTM/pull/106
[ADR-0008]: 0008-per-stage-model-routing.md
[ADR-0013]: 0013-steer-works-on-the-stored-review.md
