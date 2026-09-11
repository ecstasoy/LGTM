# 0007. Talk to models through one OpenAI-compatible adapter, with a mock fallback

- Status: Accepted
- Date: 2026-05-28 (mock fallback 2026-05-29)
- Recorded: 2026-09-11 (retroactive)

## Context

The first model provider was DeepSeek, which speaks the OpenAI chat completions protocol. [#7] implemented streaming against that protocol with the Go standard library and judged an SDK unnecessary. [#13] handled a missing or misconfigured key: instead of refusing to start, the server logs an explicit warning and uses a mock provider, so the rest of the product runs without a key.

## Decision

- `llm.Provider` has one real adapter, `llm.OpenAIProvider`, which speaks OpenAI-compatible streaming chat completions over `net/http`.
- Other vendors are reached by pointing a model profile at their OpenAI-compatible endpoint ([#111]).
- When a profile's API key is missing, that profile uses `llm.MockProvider`, and startup logs a warning.

## Alternatives considered

- **Vendor SDKs:** not used; the standard library was enough ([#7]).
- **Refusing to start without a key:** rejected in [#13] in favour of an explicit warning.

## Consequences

- Any OpenAI-compatible endpoint works without code changes. A provider with a different protocol needs a new adapter.
- Timeouts, retries and error mapping are ours to write, because no SDK supplies them.
- A production deployment with a missing key serves mock reviews, and only a log line says so. *Inferred.*

## Sources

- [#7] OpenAI-compatible provider with streaming
- [#13] explicit fallback for a misconfigured provider
- [#111] named model profiles across providers

[#7]: https://github.com/ecstasoy/LGTM/pull/7
[#13]: https://github.com/ecstasoy/LGTM/pull/13
[#111]: https://github.com/ecstasoy/LGTM/pull/111
