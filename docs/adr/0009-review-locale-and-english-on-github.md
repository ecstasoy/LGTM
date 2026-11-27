# 0009. Reviews are written in the requester's locale; GitHub gets English

- Status: Accepted
- Date: 2026-08-13 (web locale switching 2026-08-12)
- Recorded: 2026-09-11 (retroactive)

## Context

LGTM started Chinese-only. Supporting English meant deciding how the UI switches language, how a review gets generated in another language, and what language the bot uses on GitHub, where the readers are the pull request's participants rather than the LGTM user.

## Decision

- **Web UI:** the locale is kept in a cookie and served from two dictionaries, with no i18n library and no locale-prefixed routes ([#120]).
- **Reviews:** a review's locale comes from the request body, then `Accept-Language`, then `DEFAULT_LOCALE`. Webhook runs have no requester and use `DEFAULT_LOCALE`. Prompts are written natively for each locale rather than translated at run time, and a test keeps the template sets in step ([#123]). Locale is part of a review's identity ([ADR-0004]).
- **Errors:** the backend sends stable error codes and the frontend owns the wording in each language ([#123], [#124]).
- **GitHub:** everything posted to GitHub (bot reviews, comments, adopted suggestions) is English, whatever the UI or review locale ([#125]).

## Alternatives considered

- **next-intl with `[locale]` routing:** rejected in [#120].
- **A per-repository bot language set by a file on the base branch:** deferred ([#125]).

## Consequences

- Reviews made before English existed stay in Chinese, and the UI says when a review's language differs from the UI's ([#124]).
- A web request in one locale does not reuse a webhook review made in the other.
- Some backend text is still Chinese in every locale: the steer guidance prefix, several steer info frames and the L1 metadata labels.
- A third language needs a full template set, a dictionary and a new locale value.

## Sources

- [#120] locale switching and translated app shell
- [#123] reviews generated in the requested locale
- [#124] English reviews delivered end to end
- [#125] GitHub comments in English

[#120]: https://github.com/ecstasoy/LGTM/pull/120
[#123]: https://github.com/ecstasoy/LGTM/pull/123
[#124]: https://github.com/ecstasoy/LGTM/pull/124
[#125]: https://github.com/ecstasoy/LGTM/pull/125
[ADR-0004]: 0004-review-identity-and-cache-key.md
