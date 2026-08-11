import type {
  AgentToolCall,
  BudgetReport,
  File,
  PrMeta,
  Risk,
  Suggestion,
} from "./types";

// 重导出方便老消费者继续 import 自此处；新代码请直接从 ./types 取
export type { PrMeta } from "./types";

export interface StreamCallbacks {
  onPr?: (pr: PrMeta) => void;
  onFiles?: (files: File[]) => void;
  onBudgetReport?: (budget: BudgetReport) => void;
  onSummaryDelta?: (delta: string) => void;
  onRisksDone?: (risks: Risk[]) => void;
  onSuggestionsDone?: (suggestions: Suggestion[]) => void;
  onSteeredRisks?: (risks: Risk[]) => void;
  onSteeredSuggestions?: (suggestions: Suggestion[]) => void;
  // agent loop（A3 后端 emit）
  onToolCallStart?: (call: AgentToolCall) => void;
  onToolCallDone?: (call: AgentToolCall) => void;
  onAgentTextDelta?: (delta: string) => void;
  onInfo?: (message: string) => void;
  onStageError?: (stage: string, message: string) => void;
  onStageDone?: (stage: string) => void;
  onDone?: () => void;
  // review_id：流式评审 persist 后后端发；前端拿来在 /review/streaming 页启用 adopt + steer 按钮
  // 没这帧前端只能等用户回首页点列表条目；详见 PR #91
  onReviewID?: (id: string) => void;
}

// SSE_IDLE_TIMEOUT_MS 两帧之间的最长等待。summary 边生成边推帧，但 risks / suggestions
// 在计算期间静默（算完才发一帧），所以放宽到 90s：既能兜住真正卡死的连接，又不误杀
// 慢但正常的大 PR。超时后 page 显示可重试的超时提示，而非永远卡在「生成中」。
const SSE_IDLE_TIMEOUT_MS = 90_000;

// SSETimeoutError 流在 SSE_IDLE_TIMEOUT_MS 内无新帧；lib/errors friendlyError 据消息翻成中文。
export class SSETimeoutError extends Error {
  constructor() {
    super("sse idle timeout");
    this.name = "SSETimeoutError";
  }
}

// SteerMode 决定 steer 端点跑哪条路径：
//   - "stage"（默认）：重跑 risks / suggestions stage，结果替换前端 state（v2 行为）
//   - "agent"：跑 agent.Run + ReAct loop + 工具调用，结果作 info 帧（A5）；前端会看到 tool_call 时间线步骤
export type SteerMode = "stage" | "agent";

// streamSteer POST /api/review/:id/steer 引导重跑 stage 或跑 agent loop。
// 与 streamReview 一样按 SSE 帧分发；4xx/5xx 同步错误直接 throw。
export async function streamSteer(
  reviewId: string,
  text: string,
  stage: "risks" | "suggestions",
  cb: StreamCallbacks,
  signal?: AbortSignal,
  mode: SteerMode = "stage",
): Promise<void> {
  const res = await fetch(`/api/review/${encodeURIComponent(reviewId)}/steer`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, stage, mode }),
    signal,
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    const msg = (data as { error?: string }).error ?? `HTTP ${res.status}`;
    throw new Error(msg);
  }
  if (!res.body) {
    throw new Error("响应无 body");
  }
  await consume(res.body, cb);
}

// streamReview POST /api/review，按 SSE 帧分发到对应回调。
// 4xx/5xx 同步错误直接 throw；流中各 stage 错误走 onStageError。
export async function streamReview(
  url: string,
  cb: StreamCallbacks,
  signal?: AbortSignal,
  model?: string,
  stageModels?: Record<string, string>,
): Promise<void> {
  const payload: { url: string; model?: string; stage_models?: Record<string, string> } = { url };
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
    const msg = (data as { error?: string }).error ?? `HTTP ${res.status}`;
    throw new Error(msg);
  }
  if (!res.body) {
    throw new Error("响应无 body");
  }
  await consume(res.body, cb);
}

// consume 共用读流 / 切帧 / 派发循环；streamReview 与 streamSteer 都走这里
async function consume(body: ReadableStream<Uint8Array>, cb: StreamCallbacks): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buf = "";

  try {
    while (true) {
      const { done, value } = await readWithIdleTimeout(reader, SSE_IDLE_TIMEOUT_MS);
      if (done) break;
      buf += decoder.decode(value, { stream: true });

      // 按 \n\n 切帧；最后一段可能是不完整的，留在 buf
      const parts = buf.split("\n\n");
      buf = parts.pop() ?? "";

      for (const frame of parts) {
        const parsed = parseFrame(frame);
        if (parsed) dispatch(parsed, cb);
      }
    }
  } finally {
    // 超时 cancel 后底层 read() 可能仍 pending，releaseLock 会抛；忽略即可
    try {
      reader.releaseLock();
    } catch {
      /* noop */
    }
  }
}

// readWithIdleTimeout 给 reader.read() 套空闲超时：超过 ms 无新帧则 cancel 流并抛 SSETimeoutError。
// cancel 让上游 fetch 连接尽快释放，避免卡死的连接挂着不收。
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
    return; // 非法 JSON 跳过
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
    case "info": {
      const p = parsed as { message?: string };
      if (p.message) cb.onInfo?.(p.message);
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
