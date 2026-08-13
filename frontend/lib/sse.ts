import type { Dict } from "./i18n/dictionaries/zh";
import type { Locale } from "./i18n/locale";
import { ApiError } from "./errors";
import type {
  AgentToolCall,
  BudgetReport,
  File,
  PrMeta,
  Risk,
  Suggestion,
} from "./types";

// Re-exported so existing consumers can keep importing it from here; new code should import from ./types directly.
export type { PrMeta } from "./types";

// Shaped like a React ref (deliberately not importing React's RefObject type here — this file has
// no other React dependency). The caller passes a ref whose .current it keeps updated on every
// render; streamReview/streamSteer dereference it only at the moment they actually need to throw,
// not when the call is first made, so a locale switch mid-request can't leave a stale dictionary
// captured in the closure. Same technique as lib/perms.ts's tRef, just supplied by the caller
// instead of created internally, since these are plain async functions, not hooks.
export interface DictRef {
  readonly current: Dict;
}

export interface StreamCallbacks {
  onPr?: (pr: PrMeta) => void;
  onFiles?: (files: File[]) => void;
  onBudgetReport?: (budget: BudgetReport) => void;
  onSummaryDelta?: (delta: string) => void;
  onRisksDone?: (risks: Risk[]) => void;
  onSuggestionsDone?: (suggestions: Suggestion[]) => void;
  onSteeredRisks?: (risks: Risk[]) => void;
  onSteeredSuggestions?: (suggestions: Suggestion[]) => void;
  // Agent loop (emitted by the backend in A3).
  onToolCallStart?: (call: AgentToolCall) => void;
  onToolCallDone?: (call: AgentToolCall) => void;
  onAgentTextDelta?: (delta: string) => void;
  // agent_reply: the agent loop's final answer, as structured fields (Task 19) rather than the
  // baked-in-Chinese "Agent 完成（N 步）：..." string the legacy `info` frame still carries.
  onAgentReply?: (steps: number, output: string) => void;
  // code (optional): the backend's stable machine-readable code for this info frame, if any (e.g.
  // review.go's empty_pr notice). Consumers should resolve display text through friendlyError(message,
  // t, code) rather than rendering `message` raw, so a coded info frame still localizes.
  onInfo?: (message: string, code?: string) => void;
  onStageError?: (stage: string, message: string) => void;
  onStageDone?: (stage: string) => void;
  onDone?: () => void;
  // review_id: sent by the backend once a streamed review has persisted; the frontend uses it to
  // enable the adopt + steer buttons on the /review/streaming page.
  // Without this frame the frontend can only wait for the user to go back home and click the list entry; see PR #91.
  onReviewID?: (id: string) => void;
}

// SSE_IDLE_TIMEOUT_MS: the longest gap allowed between frames. The summary streams as it's
// generated, but risks/suggestions go quiet while computing (one frame once done), so this is
// relaxed to 90s: long enough to tolerate a big-but-healthy PR, short enough to catch a truly
// stuck connection. On timeout the page shows a retryable timeout message instead of hanging
// on "generating" forever.
const SSE_IDLE_TIMEOUT_MS = 90_000;

// SSETimeoutError: the stream produced no new frame within SSE_IDLE_TIMEOUT_MS.
// lib/errors' friendlyError maps this message onto a localized string.
export class SSETimeoutError extends Error {
  constructor() {
    super("sse idle timeout");
    this.name = "SSETimeoutError";
  }
}

// SteerMode picks which path the steer endpoint takes:
//   - "stage" (default): reruns the risks/suggestions stage, replacing frontend state (v2 behavior)
//   - "agent": runs agent.Run + the ReAct loop + tool calls, results arrive as info frames (A5);
//     the frontend shows a tool-call timeline
export type SteerMode = "stage" | "agent";

// streamSteer POSTs /api/review/:id/steer to rerun a stage or run the agent loop.
// Dispatches SSE frames the same way as streamReview; a synchronous 4xx/5xx throws an ApiError
// directly. `locale` is sent for the same reason streamReview sends it: backend/internal/api/steer.go
// resolves body.Locale through the same resolveLocale chain as the review endpoint, and the frontend's
// active locale (its own cookie) is independent of the browser's Accept-Language fallback — without
// this, a user who toggles the in-app language mid-session gets agent replies / reruns back in
// whichever language Accept-Language happens to negotiate, a second locale-resolution path this task
// exists to collapse into one source of truth.
export async function streamSteer(
  reviewId: string,
  text: string,
  stage: "risks" | "suggestions",
  cb: StreamCallbacks,
  t: DictRef,
  locale: Locale,
  signal?: AbortSignal,
  mode: SteerMode = "stage",
): Promise<void> {
  const res = await fetch(`/api/review/${encodeURIComponent(reviewId)}/steer`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, stage, mode, locale }),
    signal,
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    const { error, code } = data as { error?: string; code?: string };
    throw new ApiError(error ?? `HTTP ${res.status}`, code);
  }
  if (!res.body) {
    throw new Error(t.current.errors.emptyResponseBody);
  }
  await consume(res.body, cb);
}

// streamReview POSTs /api/review and dispatches the resulting SSE frames to the matching callback.
// A synchronous 4xx/5xx throws an ApiError directly; per-stage errors within the stream go through
// onStageError. `locale` is sent so the backend renders stage output (and its own error strings) in
// the tab's active language; the backend falls back to Accept-Language then DEFAULT_LOCALE if omitted.
export async function streamReview(
  url: string,
  cb: StreamCallbacks,
  t: DictRef,
  locale: Locale,
  signal?: AbortSignal,
  model?: string,
  stageModels?: Record<string, string>,
): Promise<void> {
  const payload: {
    url: string;
    model?: string;
    stage_models?: Record<string, string>;
    locale: Locale;
  } = { url, locale };
  if (model) payload.model = model;
  if (stageModels && Object.keys(stageModels).length > 0) payload.stage_models = stageModels;
  const res = await fetch("/api/review", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
    signal,
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    const { error, code } = data as { error?: string; code?: string };
    throw new ApiError(error ?? `HTTP ${res.status}`, code);
  }
  if (!res.body) {
    throw new Error(t.current.errors.emptyResponseBody);
  }
  await consume(res.body, cb);
}

// consume: the shared read/frame-split/dispatch loop used by both streamReview and streamSteer.
async function consume(body: ReadableStream<Uint8Array>, cb: StreamCallbacks): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buf = "";

  try {
    while (true) {
      const { done, value } = await readWithIdleTimeout(reader, SSE_IDLE_TIMEOUT_MS);
      if (done) break;
      buf += decoder.decode(value, { stream: true });

      // Split frames on \n\n; the last chunk may be incomplete, so it stays in buf.
      const parts = buf.split("\n\n");
      buf = parts.pop() ?? "";

      for (const frame of parts) {
        const parsed = parseFrame(frame);
        if (parsed) dispatch(parsed, cb);
      }
    }
  } finally {
    // After a timeout cancel, the underlying read() may still be pending and releaseLock() can throw; ignore it.
    try {
      reader.releaseLock();
    } catch {
      /* noop */
    }
  }
}

// readWithIdleTimeout wraps reader.read() with an idle timeout: if no new frame arrives within
// ms, it cancels the stream and throws SSETimeoutError. Cancelling lets the upstream fetch
// connection release promptly instead of hanging around unread.
async function readWithIdleTimeout(
  reader: ReadableStreamDefaultReader<Uint8Array>,
  ms: number,
): Promise<ReadableStreamReadResult<Uint8Array>> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<never>((_, reject) => {
    timer = setTimeout(() => {
      void reader.cancel(new SSETimeoutError()).catch(() => {});
      reject(new SSETimeoutError());
    }, ms);
  });
  try {
    return await Promise.race([reader.read(), timeout]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

interface ParsedFrame {
  type: string;
  data: string;
}

function parseFrame(raw: string): ParsedFrame | null {
  let type = "";
  let data = "";
  for (const line of raw.split("\n")) {
    if (line.startsWith("event: ")) {
      type = line.slice(7).trim();
    } else if (line.startsWith("data: ")) {
      data += line.slice(6);
    }
  }
  return type ? { type, data } : null;
}

function dispatch(ev: ParsedFrame, cb: StreamCallbacks): void {
  let parsed: unknown;
  try {
    parsed = JSON.parse(ev.data);
  } catch {
    return; // skip invalid JSON
  }
  switch (ev.type) {
    case "pr":
      cb.onPr?.(parsed as PrMeta);
      break;
    case "files":
      cb.onFiles?.(parsed as File[]);
      break;
    case "budget_report":
      cb.onBudgetReport?.(parsed as BudgetReport);
      break;
    case "summary_delta": {
      const p = parsed as { delta?: string };
      if (p.delta) cb.onSummaryDelta?.(p.delta);
      break;
    }
    case "risks_done":
      cb.onRisksDone?.(parsed as Risk[]);
      break;
    case "suggestions_done":
      cb.onSuggestionsDone?.(parsed as Suggestion[]);
      break;
    case "steered_risks_done":
      cb.onSteeredRisks?.(parsed as Risk[]);
      break;
    case "steered_suggestions_done":
      cb.onSteeredSuggestions?.(parsed as Suggestion[]);
      break;
    case "tool_call_start":
      cb.onToolCallStart?.(parsed as AgentToolCall);
      break;
    case "tool_call_done":
      cb.onToolCallDone?.(parsed as AgentToolCall);
      break;
    case "agent_text_delta": {
      const p = parsed as { delta?: string };
      if (p.delta) cb.onAgentTextDelta?.(p.delta);
      break;
    }
    case "agent_reply": {
      const p = parsed as { steps?: number; output?: string };
      if (p.output) cb.onAgentReply?.(p.steps ?? 0, p.output);
      break;
    }
    case "info": {
      const p = parsed as { message?: string; code?: string };
      if (p.message) cb.onInfo?.(p.message, p.code);
      break;
    }
    case "error": {
      const p = parsed as { stage?: string; message?: string };
      cb.onStageError?.(p.stage ?? "?", p.message ?? "");
      break;
    }
    case "review_id": {
      const p = parsed as { id?: string };
      if (p.id) cb.onReviewID?.(p.id);
      break;
    }
    case "done": {
      const p = parsed as { stage?: string };
      if (p.stage) {
        cb.onStageDone?.(p.stage);
      } else {
        cb.onDone?.();
      }
      break;
    }
  }
}
