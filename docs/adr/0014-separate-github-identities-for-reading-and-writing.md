# 0014. Read pull requests with the deployment token; write back as the user or the App

- Status: Accepted
- Date: 2026-05-31
- Recorded: 2026-09-11 (retroactive)

## Context

The first version read pull requests with one personal access token configured on the deployment. Writing back to GitHub (posting a suggestion as a comment, committing it, posting a bot review) must happen under an identity that GitHub authorises for that repository and that people on the pull request can see.

[#60] documented a GitHub App for sign-in and webhooks. With a GitHub App, user OAuth carries no scopes: what a token may do comes from the App's permissions and the user's own access ([#87]). GitHub has no REST endpoint for applying a suggestion, so committing one goes through GraphQL ([#90]).

## Decision

- **Reading** pull requests uses the deployment's `GITHUB_TOKEN` through `github.Fetcher`, for web and webhook reviews alike.
- **Signing in** uses the GitHub App's user OAuth flow. The user's token stays on the server, in their login session.
- **Adopting a suggestion** acts as the signed-in user, with their token. A comment needs triage permission or higher on the base repository; a commit needs write ([#88], [#89], [#90]). A commit posts the comment, then applies it with `applyPullRequestReviewThreadSuggestion`.
- **Bot reviews** use a GitHub App installation token and never approve ([#92]).
- Without App or OAuth configuration, the deployment falls back to reading with the token alone.

## Alternatives considered

- **Installation tokens for reading:** planned in `docs/GITHUB_APP.md` to replace the personal token; not implemented.

## Consequences

- LGTM can review whatever the deployment token can read, including for anonymous visitors ([ADR-0002]), and cannot review a repository the token cannot read even where the App is installed. *Inferred.*
- GitHub has the final say on a commit, based on the user's rights on the head branch. On a fork that disallows maintainer edits, the comment is posted and the commit fails ([#90]).
- Two GitHub clients coexist: go-github for reading, and hand-written HTTP and GraphQL calls in `backend/internal/oauth` for everything else.
- `docs/github-app-manifest.yml` lists `contents: read` and no `issue_comment` event. Check both against the App's real settings before relying on the manifest.

## Sources

- [#60] GitHub App manifest and setup guide
- [#87] GitHub App OAuth sign-in and login sessions
- [#88] per-repository permission checks
- [#89] adopt a suggestion as a comment
- [#90] adopt a suggestion as a commit through GraphQL
- [#92] bot review with an installation token

[#60]: https://github.com/ecstasoy/LGTM/pull/60
[#87]: https://github.com/ecstasoy/LGTM/pull/87
[#88]: https://github.com/ecstasoy/LGTM/pull/88
[#89]: https://github.com/ecstasoy/LGTM/pull/89
[#90]: https://github.com/ecstasoy/LGTM/pull/90
[#92]: https://github.com/ecstasoy/LGTM/pull/92
[ADR-0002]: 0002-anyone-can-start-a-review.md
