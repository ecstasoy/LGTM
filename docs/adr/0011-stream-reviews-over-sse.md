# 0011. Stream review runs to the browser over server-sent events

- Status: Accepted
- Date: 2026-05-29
- Recorded: 2026-09-11 (retroactive)

## Context

A full review run takes tens of seconds. Returned as one response, the first content took about 15 seconds to appear; streaming cut the time to first bytes to about 200 ms ([#16]).

## Decision

- `POST /api/review` and `POST /api/review/:id/steer` answer with `text/event-stream`: one frame per stage event or run-level event, ending with exactly one final `done` frame.
- A request that fails validation gets a plain JSON error before the stream opens.
- The browser reads the stream with `fetch` and its own parser (`frontend/lib/sse.ts`), because `EventSource` supports only GET.
- In production, Vercel rewrites send these requests straight to the backend on Fly, avoiding edge function timeouts (README).

## Alternatives considered

None recorded. WebSockets and polling do not appear in the pull request record.

## Consequences

- Frame names and payloads are a contract between `backend/internal/api` and `frontend/lib/sse.ts`, with no shared schema.
- Each stage emits its own `done` event, so handlers must suppress all but one.
- A stalled stream looks like a slow one; the frontend needed an idle timeout with a retry ([#108]).
- Development proxies that buffer responses break streaming ([#16]).
- Other live updates, such as notifications, poll instead.

## Sources

- [#16] `/api/review` streamed over SSE end to end
- [#108] stream stall timeout, localized errors and retry in the frontend

[#16]: https://github.com/ecstasoy/LGTM/pull/16
[#108]: https://github.com/ecstasoy/LGTM/pull/108
