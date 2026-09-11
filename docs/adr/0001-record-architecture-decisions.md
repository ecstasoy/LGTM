# 0001. Record architecture decisions

- Status: Accepted
- Date: 2026-09-11

## Context

LGTM's significant design decisions were made across more than a hundred pull requests. Their reasons live in PR descriptions, commit messages and code comments, where they are hard to find and easy to contradict. Some have already been argued twice: requiring sign-in to start a review shipped and was reverted the same day. Some documents, such as `docs/DEPLOY.md`, have drifted from the running system.

## Decision

We record architecturally significant decisions as ADRs in `docs/adr/`.

- One decision per file, named `NNNN-short-title.md`, numbered in order and based on `0000-template.md`.
- A decision is significant when it is hard to reverse, sets a security or cost boundary, changes data identity or a public contract, or would surprise a new maintainer.
- An accepted ADR is not rewritten. A changed decision gets a new ADR, and the old one's status becomes `Superseded by NNNN`. Fixing typos, links and factual errors is fine.
- A pull request that makes or changes such a decision adds or supersedes the ADR in the same pull request.
- Domain vocabulary lives in `CONTEXT.md`, and ADRs use its terms.
- ADRs 0002–0015 were recorded retroactively on 2026-09-11. Their reasons come from the pull requests each one cites. A reason nobody wrote down at the time is marked *inferred*.

## Alternatives considered

- **Keep reasons in PR descriptions only:** that is the status quo this replaces; it does not survive a search for "why is it like this".

## Consequences

- Architecture reviews and new contributors can check what was decided, and why, before proposing a change.
- Decision-making pull requests carry one more file.
- A retroactive ADR is only as good as the PR record behind it, and says so where that record is thin.
