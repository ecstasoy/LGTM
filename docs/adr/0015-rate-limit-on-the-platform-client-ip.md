# 0015. Rate-limit per client IP, taken from the platform's header

- Status: Accepted
- Date: 2026-05-30 (client IP source revised 2026-08-29)
- Recorded: 2026-09-11 (retroactive)

## Context

With anonymous reviews ([ADR-0002]), a rate limit is what stands between a visitor and the model budget. [#53] added a per-IP limiter on expensive and read endpoints, and [#59] moved its counters onto `store.Cache` so instances share them. A fixed window fits the cache's atomic increment, and a limiter should not take the service down because Redis hiccuped, so it fails open ([#59]).

On Fly, every request arrives through Fly's proxy. The deployment's `TRUSTED_PROXIES="fly-global-services"` was never parsed as a proxy, so every visitor shared a single bucket. There is no stable address range to trust instead, but Fly overwrites the `Fly-Client-IP` header on every request ([#130]).

## Decision

- Requests are counted per client IP, in fixed windows held in `store.Cache`: 5 per 25 seconds on expensive endpoints (review, steer, adopting a suggestion) and 20 per 4 seconds on read endpoints. Webhooks and health checks are not limited.
- Over the limit, a request gets 429 with `Retry-After` and error code `rate_limited`.
- If the cache cannot count, the request goes through.
- The client IP comes from the header named in `TRUSTED_PLATFORM` when it is set (`Fly-Client-IP` in production), otherwise through `TRUSTED_PROXIES`, otherwise from the connecting address.
- `TRUSTED_PLATFORM` is empty by default, because trusting a header a client can forge is worse than a shared bucket. The setting is generic so other platforms can name their own header, such as `CF-Connecting-IP` ([#130]).

## Alternatives considered

- **A corrected list of Fly proxy addresses:** rejected in [#130]; there is no stable range to list.
- **Limits keyed on the login session:** deferred ([#59], [#130]).

## Consequences

- One IP is one bucket: people behind the same NAT share a limit, and one person with many addresses gets many.
- While the cache is failing there is no rate limit at all; [#130] flagged fail-open for review.
- A deployment behind a proxy that sets neither `TRUSTED_PLATFORM` nor `TRUSTED_PROXIES` correctly puts every visitor in one bucket, as production did before [#130].

## Sources

- [#53] per-IP rate limiter on expensive and read endpoints
- [#59] rate limit counters in `store.Cache`
- [#130] rate limits keyed on the real client IP behind Fly's proxy

[#53]: https://github.com/ecstasoy/LGTM/pull/53
[#59]: https://github.com/ecstasoy/LGTM/pull/59
[#130]: https://github.com/ecstasoy/LGTM/pull/130
[ADR-0002]: 0002-anyone-can-start-a-review.md
