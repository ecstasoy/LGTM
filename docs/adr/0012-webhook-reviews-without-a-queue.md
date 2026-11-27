# 0012. Run webhook reviews in the background, without a queue

- Status: Accepted
- Date: 2026-05-31
- Recorded: 2026-09-11 (retroactive)

## Context

Once the GitHub App is installed on a repository, reviews should start without anyone visiting LGTM. A review run takes 30 seconds or more, longer than GitHub waits for a webhook response before counting the delivery as failed ([#92]). GitHub also redelivers events, so the same event can arrive more than once.

## Decision

- `POST /api/webhook/github` verifies the HMAC signature and answers `202 Accepted` at once.
- It reacts to `pull_request` events with action `opened`, `synchronize` or `reopened`, and to pull request comments carrying a slash command (`/lgtm`, `/lgtm review`, `/lgtm help`). Comments from `[bot]` accounts are ignored so the bot cannot trigger itself ([#93]).
- The review run happens in a goroutine with a five-minute timeout. There is no queue and no retry.
- If a stored review already matches the event's key ([ADR-0004]), the event is skipped.
- The bot posts one pull request review with event `COMMENT` and inline suggestions. It never approves ([#92]).
- The event's sender and the pull request's author get an in-app notification.

## Alternatives considered

- **A Redis queue with retries:** deferred; GitHub's own redelivery was judged enough ([#92]).

## Consequences

- A deploy or crash during a webhook review loses it silently; nothing records that the event was accepted.
- Webhook reviews are owned by the pull request's author, and the skip check matches only anonymous reviews, so a repeated delivery runs the review again.
- `docs/github-app-manifest.yml` does not subscribe to `issue_comment`, which slash commands need.
- Comments in `api.WebhookGitHub` still describe the first version (only `opened`, answering 200).
- Webhook requests are not rate limited; the signature check is their guard.

## Sources

- [#92] auto-review on `pull_request.opened`, bot review and notifications
- [#93] `synchronize`, `reopened` and `/lgtm` slash commands

[#92]: https://github.com/ecstasoy/LGTM/pull/92
[#93]: https://github.com/ecstasoy/LGTM/pull/93
[ADR-0004]: 0004-review-identity-and-cache-key.md
