# 0008. Route each stage to its own model through a named model registry

- Status: Accepted
- Date: 2026-06-15 (runtime choice 2026-06-16, per-stage choice on the web 2026-08-11)
- Recorded: 2026-09-11 (retroactive)

## Context

The three stages need different things: risks benefit from a reasoning model, while the summary can use a fast one ([#110]). One pass per stage keeps cost predictable. Deployments also want models from several vendors side by side ([#111]), and users want to pick a model when they start a review ([#112], [#116]).

## Decision

- Models are model profiles in the model registry, configured with `LLM_MODELS`. A profile names its API key through an environment variable (`api_key_env`), so keys stay out of config and logs.
- Each stage resolves its model from, in order: the request's per-stage choice, the request's single choice, the deployment's stage models, the registry default.
- A request may choose only models the registry publishes at `GET /api/models`; anything else is rejected with `unknown_model`. The allowlist is the cost and security gate ([#112]).
- A run that uses any non-default model skips the review cache: it neither replays nor stores a review. This avoided a schema migration to add the model to the cache key ([#112]).

## Alternatives considered

- **An extra, separate pass for breaking changes:** rejected in [#110]; each stage stays one pass.
- **A mandatory registry:** rejected in [#111] in favour of falling back to the single configured provider, which kept existing tests unchanged.
- **Model in the cache key:** deferred ([#112], [#116]).

## Consequences

- A review made with a non-default model is not stored, so it is not in history and cannot be reopened.
- Steer uses the deployment's stage models, not the models the original review used, and agent runs use the default provider.
- `llm.Registry.Resolve` treats an unknown key as a raw model name on the default provider, so only the request handler's allowlist check stops unknown models.
- The token budget is fixed ([ADR-0010]) whatever context window the routed model has.
- PR titles and some code comments call these three steps "L1/L2/L3", which collides with context layers. `CONTEXT.md` reserves L1–L4 for context layers.

## Sources

- [#110] per-stage model routing
- [#111] multi-provider model registry with named profiles
- [#112] runtime model choice from an allowlist
- [#116] per-stage model choice on the web

[#110]: https://github.com/ecstasoy/LGTM/pull/110
[#111]: https://github.com/ecstasoy/LGTM/pull/111
[#112]: https://github.com/ecstasoy/LGTM/pull/112
[#116]: https://github.com/ecstasoy/LGTM/pull/116
[ADR-0010]: 0010-layered-pr-context-under-a-token-budget.md
