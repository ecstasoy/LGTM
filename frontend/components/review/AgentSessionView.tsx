"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import {
  AlertTriangle,
  AlignLeft,
  Check,
  ExternalLink,
  GitBranch,
  GitPullRequest,
  History as HistoryIcon,
  MessageSquare,
  Send,
  Sparkle,
  Wrench,
} from "lucide-react";

import type {
  AgentToolCall,
  BudgetReport,
  File,
  PrMeta,
  Risk,
  Suggestion,
} from "@/lib/types";
import { streamSteer, type SteerMode } from "@/lib/sse";
import { cn } from "@/lib/utils";
import { FileStatusBadge } from "@/components/ui/file-status-badge";
import { SeverityBadge } from "@/components/ui/badge";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/context";
import type { Dict } from "@/lib/i18n/dictionaries/zh";

type StepStatus = "pending" | "running" | "done" | "error";

// SteerEntry 一次用户引导的记录；时间线追加到 5 步主流程之后
interface SteerEntry {
  id: string;
  text: string;
  stage: "risks" | "suggestions";
  mode: SteerMode; // stage 重跑 / agent 跑 ReAct loop
  status: "running" | "done" | "error";
  resultCount?: number;
  error?: string;
  // agent 模式：后端发的「Agent 完成（N 步）：...」最终回复正文 + 步数
  // 之前路由到页面顶部 InfoBanner，太远离 timeline；现在挂在 entry 上让 SteerDetail 内联渲染 markdown
  agentResponse?: string;
  agentSteps?: number;
}

interface Props {
  pr: PrMeta;
  files: File[];
  risks: Risk[];
  suggestions: Suggestion[];
  summary: string;
  // budget 后端真值；null 时回退到 patch-bytes 粗估（流式 budget_report 帧到达前 / 旧缓存）
  budget: BudgetReport | null;
  // 流式状态机：用于推断每步是 pending / running / done
  hasFiles: boolean;
  risksDone: boolean;
  suggestionsDone: boolean;
  streaming: boolean;
  // Steer：cached 模式才有 reviewId；streaming 模式 undefined，SteerComposer 禁用
  reviewId?: string;
  onSteeredRisks?: (risks: Risk[]) => void;
  onSteeredSuggestions?: (suggestions: Suggestion[]) => void;
  onSteerToolCallStart?: (call: AgentToolCall) => void;
  onSteerToolCallDone?: (call: AgentToolCall) => void;
  onSteerInfo?: (message: string) => void;
  // Agent loop tool 调用事件（A3 后端 emit tool_call_start/done）
  // 按 id 已合并 start/done，状态从 running → done/error；从父级 state 流下来
  toolEvents?: ToolEvent[];
}

// ToolEvent agent loop 单次工具调用的合并状态
export interface ToolEvent {
  id: string;
  name: string;
  arguments?: string;
  result?: string;
  status: "running" | "done" | "error";
}

// MergeToolEvent 给 page.tsx 复用：根据 tool_call_start / done 帧累积 ToolEvent[]
// 父组件用 reducer 累积，子组件只渲染。
export function mergeToolStart(prev: ToolEvent[], call: AgentToolCall): ToolEvent[] {
  // 同 id 二次 start 应该不会发生；防御性覆盖
  const idx = prev.findIndex((e) => e.id === call.id);
  const next: ToolEvent = {
    id: call.id,
    name: call.name,
    arguments: call.arguments,
    status: "running",
  };
  if (idx >= 0) {
    const copy = [...prev];
    copy[idx] = next;
    return copy;
  }
  return [...prev, next];
}

export function mergeToolDone(prev: ToolEvent[], call: AgentToolCall): ToolEvent[] {
  const idx = prev.findIndex((e) => e.id === call.id);
  if (idx < 0) {
    // 没见过 start 也兼容（理论不应发生）：直接当 done 插入
    return [
      ...prev,
      {
        id: call.id,
        name: call.name,
        result: call.result,
        status: call.result?.startsWith("error:") ? "error" : "done",
      },
    ];
  }
  const copy = [...prev];
  copy[idx] = {
    ...copy[idx],
    result: call.result,
    status: call.result?.startsWith("error:") ? "error" : "done",
  };
  return copy;
}

// AgentSessionView 会话视图：把评审流程显示为 5 步 agent 时间线。
// 严格对齐 design 原型 AgentSession.jsx：parse → fetch → context → llm → cache。
// 状态由父组件传入的 SSE 状态机推断；cached 模式所有步骤 done。
export function AgentSessionView({
  pr,
  files,
  risks,
  suggestions,
  summary,
  budget,
  hasFiles,
  risksDone,
  suggestionsDone,
  streaming,
  reviewId,
  onSteeredRisks,
  onSteeredSuggestions,
  onSteerToolCallStart,
  onSteerToolCallDone,
  onSteerInfo,
  toolEvents,
}: Props) {
  const t = useT();
  const scrollRef = useRef<HTMLDivElement>(null);

  // 5 步状态从 SSE 派生：
  //  - parse: pr 来即 done（顶层调用方保证 pr 不为 null 才渲染此组件）
  //  - fetch: hasFiles → done；否则 streaming 期间 running
  //  - context: files 来后立刻 running，summary 出现即 done（context 在 stages 启动前完成）
  //  - llm: summary 来 → running；suggestionsDone 后 → done
  //  - cache: 流结束（!streaming）且未报错 → done；否则 pending
  const statuses = useMemo<StepStatus[]>(() => {
    const hasSummary = summary.length > 0;
    return [
      "done", // parse 总是已完成（页面渲染时一定有 pr）
      hasFiles ? "done" : streaming ? "running" : "pending",
      hasSummary ? "done" : hasFiles ? "running" : "pending",
      suggestionsDone ? "done" : hasSummary ? "running" : "pending",
      !streaming && suggestionsDone ? "done" : "pending",
    ];
  }, [streaming, hasFiles, summary, risksDone, suggestionsDone]);

  // steer 历史：每条引导一个步骤；状态机由 streamSteer 回调驱动
  const [steerHistory, setSteerHistory] = useState<SteerEntry[]>([]);
  const [steerInFlight, setSteerInFlight] = useState(false);
  const steerInFlightRef = useRef(false);

  const handleSteerSend = useCallback(
    async (text: string, stage: "risks" | "suggestions", mode: SteerMode) => {
      if (!reviewId || steerInFlightRef.current) return;
      steerInFlightRef.current = true;
      const id = `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
      setSteerHistory((prev) => [...prev, { id, text, stage, mode, status: "running" }]);
      setSteerInFlight(true);
      const markDone = (count: number) =>
        setSteerHistory((prev) =>
          prev.map((e) => (e.id === id ? { ...e, status: "done", resultCount: count } : e)),
        );
      const markError = (msg: string) =>
        setSteerHistory((prev) =>
          prev.map((e) => (e.id === id ? { ...e, status: "error", error: msg } : e)),
        );
      try {
        await streamSteer(
          reviewId,
          text,
          stage,
          {
            onSteeredRisks: (r) => {
              onSteeredRisks?.(r);
              if (mode === "stage" && stage === "risks") markDone(r.length);
            },
            onSteeredSuggestions: (s) => {
              onSteeredSuggestions?.(s);
              if (mode === "stage" && stage === "suggestions") markDone(s.length);
            },
            onToolCallStart: (call) => onSteerToolCallStart?.(call),
            onToolCallDone: (call) => onSteerToolCallDone?.(call),
            onInfo: (message) => {
              // agent 路径 backend 发两条 info：「Agent 启动...」+「Agent 完成（N 步）：BODY」
              // 都挂到当前 entry 上，不要污染页面顶部 InfoBanner（远离 timeline 失去上下文）
              const finishMatch = message.match(/^Agent 完成（(\d+) 步）：(.*)$/s);
              if (finishMatch) {
                const steps = parseInt(finishMatch[1], 10);
                const body = finishMatch[2].trim();
                setSteerHistory((prev) =>
                  prev.map((e) =>
                    e.id === id ? { ...e, agentResponse: body, agentSteps: steps } : e,
                  ),
                );
                return;
              }
              if (message.startsWith("Agent 启动")) {
                // 启动信息已经被 running 状态隐含；丢弃避免重复显示
                return;
              }
              // 其它 info（如「评审保存中…」/ "stage error" 提示）保留页面级 InfoBanner 路径
              onSteerInfo?.(message);
            },
            onStageError: (_s, msg) => markError(msg),
          },
          undefined,
          mode,
        );
        // 兜底：极端情况下流结束仍未拿到 steered 帧，标 done 不报错
        // agent 模式不期待 steered_* 帧，工具调用结果走 toolEvents 时间线
        setSteerHistory((prev) =>
          prev.map((e) =>
            e.id === id && e.status === "running"
              ? e.mode === "stage"
                ? { ...e, status: "done", resultCount: 0 }
                : { ...e, status: "done" }
              : e,
          ),
        );
      } catch (e) {
        markError(e instanceof Error ? e.message : String(e));
      } finally {
        steerInFlightRef.current = false;
        setSteerInFlight(false);
      }
    },
    [
      reviewId,
      onSteeredRisks,
      onSteeredSuggestions,
      onSteerInfo,
      onSteerToolCallDone,
      onSteerToolCallStart,
    ],
  );

  const finished = !streaming && suggestionsDone;

  // 滚到底，让流式新增的步骤始终可见
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [statuses, finished, (toolEvents ?? []).length]);

  // budget prefers backend truth (SSE budget_report / cached detail); falls back to a patch-bytes estimate when missing
  const budgetView = useMemo(
    () => (budget ? fromBudgetReport(budget, t) : estimateBudget(pr, files, t)),
    [budget, pr, files, t],
  );

  return (
    <div className="flex h-full min-w-0 flex-col">
      <div ref={scrollRef} className="flex-1 overflow-y-auto">
        <div className="mx-auto max-w-[860px] px-6 pb-6 pt-5">
          <header className="mb-5 flex items-center gap-2.5">
            <span className="inline-flex h-[30px] w-[30px] items-center justify-center rounded-[9px] bg-accent text-accent-fg">
              <Sparkle className="h-[17px] w-[17px]" fill="currentColor" />
            </span>
            <div className="min-w-0">
              <div className="text-base font-semibold">
                {t.agent.sessionTitle(pr.owner, pr.repo, pr.pr)}
              </div>
              <div className="font-mono text-xs text-muted">{t.agent.reviewerPerspectiveNote}</div>
            </div>
          </header>

          <Step
            icon={<GitBranch className="h-3.5 w-3.5" />}
            title={t.agent.stepParseTitle}
            status={statuses[0]}
            meta={`${pr.owner}/${pr.repo}#${pr.pr}`}
            t={t}
          >
            <ParseChips pr={pr} />
          </Step>
          <Step
            icon={<GitPullRequest className="h-3.5 w-3.5" />}
            title={t.agent.stepFetchTitle}
            status={statuses[1]}
            meta={
              pr.stats
                ? `${pr.stats.files} files · +${pr.stats.additions} −${pr.stats.deletions}`
                : `${files.length} files`
            }
            t={t}
          >
            <FetchDetail files={files} pr={pr} t={t} />
          </Step>
          <Step
            icon={<AlignLeft className="h-3.5 w-3.5" />}
            title={t.agent.stepContextTitle}
            status={statuses[2]}
            meta={`${(budgetView.total / 1000).toFixed(1)}K tok${budgetView.estimated ? t.agent.estimatedSuffix : ""}`}
            t={t}
          >
            <ContextDetail budget={budgetView} t={t} />
          </Step>
          <Step
            icon={<Sparkle className="h-3.5 w-3.5" />}
            title={t.agent.stepLlmTitle}
            status={statuses[3]}
            meta={
              summary
                ? `${t.agent.riskCountPhrase(risks.length)} · ${t.agent.suggestionCountPhrase(suggestions.length)}`
                : ""
            }
            t={t}
          >
            <LlmDetail
              summary={summary}
              risks={risks}
              suggestions={suggestions}
              hasSummary={summary.length > 0}
              risksDone={risksDone}
              suggestionsDone={suggestionsDone}
              streaming={streaming}
              t={t}
            />
          </Step>
          {(toolEvents ?? []).map((evt) => (
            <Step
              key={evt.id}
              icon={<Wrench className="h-3.5 w-3.5" />}
              title={t.agent.toolCallStepTitle(evt.name)}
              status={evt.status}
              meta={toolMeta(evt, t)}
              t={t}
            >
              <ToolEventDetail evt={evt} t={t} />
            </Step>
          ))}

          <Step
            icon={<HistoryIcon className="h-3.5 w-3.5" />}
            title={t.agent.stepCacheTitle}
            status={statuses[4]}
            meta={statuses[4] === "done" ? "SQLite" : ""}
            isLast={steerHistory.length === 0}
            t={t}
          >
            <CacheDetail pr={pr} t={t} />
          </Step>

          {steerHistory.map((entry, i) => (
            <Step
              key={entry.id}
              icon={<MessageSquare className="h-3.5 w-3.5" />}
              title={t.agent.stepSteerTitle}
              status={entry.status}
              meta={steerMeta(entry, t)}
              isLast={i === steerHistory.length - 1}
              t={t}
            >
              <SteerDetail entry={entry} t={t} />
            </Step>
          ))}

          {finished ? (
            <FinalCard pr={pr} risks={risks} suggestions={suggestions} t={t} />
          ) : (
            <div className="flex items-center gap-2 pl-[38px] text-sm text-muted">
              <span className="flex gap-1">
                {[0, 1, 2].map((i) => (
                  <span
                    key={i}
                    className="inline-block h-1.5 w-1.5 rounded-full bg-muted"
                    style={{ animation: `pulse-dot 1s ${i * 0.18}s infinite` }}
                  />
                ))}
              </span>
              {t.agent.agentWorkingLabel}
            </div>
          )}
        </div>
      </div>
      <SteerComposer
        pr={pr}
        disabled={!reviewId}
        inFlight={steerInFlight}
        onSend={handleSteerSend}
        t={t}
      />
    </div>
  );
}

function steerMeta(entry: SteerEntry, t: Dict): string {
  if (entry.mode === "agent") {
    if (entry.status === "running") return `${t.agent.agentDeepDiveLabel} · ${t.agent.runningLabel}`;
    if (entry.status === "error") return `${t.agent.agentDeepDiveLabel} · ${t.agent.failedLabel}`;
    return `${t.agent.agentDeepDiveLabel} · ${t.agent.doneLabel}`;
  }
  const label = t.agent.steerStageLabel(entry.stage);
  if (entry.status === "running") return `${label} · ${t.agent.runningLabel}`;
  if (entry.status === "error") return `${label} · ${t.agent.failedLabel}`;
  return `${label} · ${t.agent.itemsCount(entry.resultCount ?? 0)}`;
}

// agentReplyProse 跟 page.tsx InfoBanner 同款紧凑 markdown 排版
const agentReplyProse =
  "[&_p]:my-1.5 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_ul]:my-1.5 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-1.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:my-0.5 [&_h1]:mt-3 [&_h1]:mb-1.5 [&_h1]:text-base [&_h1]:font-semibold [&_h2]:mt-3 [&_h2]:mb-1.5 [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:mt-2 [&_h3]:mb-1 [&_h3]:text-[13px] [&_h3]:font-semibold [&_code]:rounded [&_code]:bg-surface-2 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[12px] [&_pre]:my-2 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-surface-2 [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_strong]:font-semibold [&_strong]:text-text";

function SteerDetail({ entry, t }: { entry: SteerEntry; t: Dict }) {
  return (
    <ToolCard
      label={
        <>
          <MessageSquare className="h-3 w-3" />
          POST /api/review/:id/steer · mode = {entry.mode}
          {entry.mode === "stage" ? ` · stage = ${entry.stage}` : ""}
        </>
      }
    >
      <p className="text-sm leading-snug text-text-2">{t.agent.quotedText(entry.text)}</p>
      {entry.status === "error" && entry.error ? (
        <p className="mt-2 text-[10.5px] text-high">{t.agent.steerFailedPrefix(entry.error)}</p>
      ) : null}
      {/* agent mode: render the final reply markdown inline in the card (previously floated at the page top, away from the timeline) */}
      {entry.mode === "agent" && entry.agentResponse ? (
        <div className="mt-3 rounded-md border border-border bg-surface p-3">
          <div className="mb-2 text-[10.5px] font-medium text-muted">
            {t.review.agentReplyLabel}
            {entry.agentSteps ? t.agent.agentStepsSuffix(entry.agentSteps) : ""}
          </div>
          <div className={`text-sm text-text ${agentReplyProse}`}>
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{entry.agentResponse}</ReactMarkdown>
          </div>
        </div>
      ) : null}
      {entry.status === "done" && !(entry.mode === "agent" && entry.agentResponse) ? (
        <p className="mt-2 text-[10.5px] text-faint">
          {entry.mode === "agent"
            ? t.agent.steerAgentCompletedNote
            : t.agent.steerReplacedListNote(entry.stage, entry.resultCount ?? 0)}
        </p>
      ) : null}
    </ToolCard>
  );
}

// toolMeta: meta text shown on the right of an agent tool-call step
function toolMeta(evt: ToolEvent, t: Dict): string {
  if (evt.status === "running") return t.agent.runningLabel;
  if (evt.status === "error") return t.agent.failedLabel;
  const len = evt.result?.length ?? 0;
  if (len > 1000) return `${(len / 1000).toFixed(1)}K chars`;
  return `${len} chars`;
}

// ToolEventDetail: tool-call detail card — arguments + result (rendered as markdown, scrollable)
// remarkBreaks keeps single line breaks as <br>, so patch / grep output's line structure isn't merged by CommonMark
// Long results use max-h + overflow-y-auto to show the full content instead of hard-truncating
function ToolEventDetail({ evt, t }: { evt: ToolEvent; t: Dict }) {
  const argsPreview = evt.arguments?.slice(0, 200) ?? "";
  const result = evt.result ?? "";
  return (
    <ToolCard
      label={
        <>
          <Wrench className="h-3 w-3" />
          {evt.name}
          {evt.arguments ? (
            <span className="ml-2 font-mono text-faint">args: {argsPreview}</span>
          ) : null}
        </>
      }
    >
      {evt.status === "running" ? (
        <p className="text-xs text-muted">{t.agent.toolExecutingLabel}</p>
      ) : evt.status === "error" ? (
        <p className="text-[11px] text-high">{result}</p>
      ) : (
        <div className="max-h-[400px] overflow-y-auto pr-1 text-[11px] leading-snug text-text-2 [&_code]:rounded [&_code]:bg-surface-2 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[10.5px] [&_pre]:my-1.5 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-surface-2 [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_h1]:mt-2 [&_h1]:text-xs [&_h1]:font-semibold [&_h2]:mt-2 [&_h2]:text-xs [&_h2]:font-semibold [&_h3]:mt-2 [&_h3]:text-[11px] [&_h3]:font-semibold [&_hr]:my-2 [&_hr]:border-border [&_li]:my-0.5 [&_p]:my-1 [&_strong]:font-medium [&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-4 [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-4">
          <ReactMarkdown remarkPlugins={[remarkGfm, remarkBreaks]}>
            {result}
          </ReactMarkdown>
        </div>
      )}
    </ToolCard>
  );
}

// ─────────────── Step shell ───────────────

function Step({
  icon,
  title,
  status,
  meta,
  isLast,
  children,
  t,
}: {
  icon: React.ReactNode;
  title: string;
  status: StepStatus;
  meta?: string;
  isLast?: boolean;
  children?: React.ReactNode;
  t: Dict;
}) {
  const isDone = status === "done";
  const isRunning = status === "running";
  const isPending = status === "pending";
  const isError = status === "error";
  return (
    <div className="grid grid-cols-[26px_1fr] gap-3">
      <div className="flex flex-col items-center">
        <span
          className={cn(
            "inline-flex h-[26px] w-[26px] shrink-0 items-center justify-center rounded-md border",
            isPending
              ? "border-border bg-surface-2 text-faint"
              : isRunning
                ? "border-border-strong bg-surface text-accent"
                : isError
                  ? "border-high-bd bg-high-bg text-high"
                  : "border-border-strong bg-surface text-ok",
          )}
        >
          {isRunning ? (
            <Spinner size="xs" className="text-accent" />
          ) : isError ? (
            <AlertTriangle className="h-3.5 w-3.5" />
          ) : (
            icon
          )}
        </span>
        {!isLast ? (
          <span className="my-1 min-h-3 w-[1.5px] flex-1 bg-border" />
        ) : null}
      </div>
      <div className={cn("min-w-0", isLast ? "pb-0" : "pb-4")}>
        <div className="flex min-h-[26px] items-center gap-2">
          <span
            className={cn(
              "whitespace-nowrap text-sm font-semibold",
              isPending ? "text-muted" : "text-text",
            )}
          >
            {title}
          </span>
          {isRunning ? (
            <span className="whitespace-nowrap font-mono text-[10px] text-accent">
              {t.agent.runningEllipsisLabel}
            </span>
          ) : null}
          {meta && (isDone || isError) ? (
            <span
              className={cn(
                "ml-auto whitespace-nowrap font-mono text-[10.5px]",
                isError ? "text-high" : "text-faint",
              )}
            >
              {meta}
            </span>
          ) : null}
        </div>
        {!isPending && children ? <div className="mt-2">{children}</div> : null}
      </div>
    </div>
  );
}

function ToolCard({
  label,
  children,
}: {
  label?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="overflow-hidden rounded-md border border-border bg-surface-2">
      {label ? (
        <div className="flex items-center gap-1.5 border-b border-border px-2.5 py-1.5 font-mono text-[10.5px] text-muted">
          {label}
        </div>
      ) : null}
      <div className="p-2.5">{children}</div>
    </div>
  );
}

// ─────────────── Step detail renderers ───────────────

function ParseChips({ pr }: { pr: PrMeta }) {
  const entries: Array<[string, string]> = [
    ["owner", pr.owner],
    ["repo", pr.repo],
    ["pr", `#${pr.pr}`],
    ["head", pr.head_sha.slice(0, 11)],
  ];
  return (
    <div className="flex flex-wrap gap-1.5">
      {entries.map(([k, v]) => (
        <code
          key={k}
          className="rounded-md border border-border bg-surface-2 px-2 py-0.5 font-mono text-[10.5px]"
        >
          <span className="text-faint">{k}</span> {v}
        </code>
      ))}
    </div>
  );
}

function FetchDetail({ files, pr, t }: { files: File[]; pr: PrMeta; t: Dict }) {
  if (files.length === 0) {
    return <p className="text-xs text-muted">{t.agent.noFileChangesNote}</p>;
  }
  return (
    <ToolCard
      label={
        <>
          <GitPullRequest className="h-3 w-3" /> GET /repos/{pr.owner}/{pr.repo}/pulls/{pr.pr}/files
        </>
      }
    >
      <div className="flex flex-col gap-1">
        {files.slice(0, 10).map((f) => (
          <div key={f.path} className="flex items-center gap-2">
            <FileStatusBadge status={f.status} />
            <code className="min-w-0 flex-1 truncate font-mono text-xs">{f.path}</code>
            <span className="flex shrink-0 gap-1 font-mono text-[10px]">
              <span className="text-ok">+{f.additions}</span>
              <span className="text-high">−{f.deletions}</span>
            </span>
          </div>
        ))}
        {files.length > 10 ? (
          <p className="pt-1 text-[10px] text-faint">{t.agent.moreFilesNote(files.length - 10)}</p>
        ) : null}
      </div>
    </ToolCard>
  );
}

interface BudgetItem {
  layer: "L1" | "L2" | "L3" | "L4";
  label: string;
  tok: number;
  className: string;
}

interface BudgetView {
  items: BudgetItem[];
  total: number;
  dropped: string[];
  estimated: boolean; // true = 后端真值缺失时的粗估
}

// budgetLabels: L1 is an API/format term (kept as-is); L2-L4 are user-facing copy, so they come from the dictionary
function budgetLabels(t: Dict) {
  return {
    L1: "diff hunk + PR meta",
    L2: t.agent.budgetLabelL2,
    L3: t.agent.budgetLabelL3,
    L4: t.agent.budgetLabelL4,
  } as const;
}

// fromBudgetReport: backend truth (budget_report SSE frame / cached detail) -> render shape
// L4 only shows when used_l4 > 0, so older reviews without RAG don't get an empty row
function fromBudgetReport(b: BudgetReport, t: Dict): BudgetView {
  const labels = budgetLabels(t);
  const items: BudgetItem[] = [
    { layer: "L1", label: labels.L1, tok: b.used_l1, className: "bg-info" },
    { layer: "L2", label: labels.L2, tok: b.used_l2, className: "bg-ok" },
    { layer: "L3", label: labels.L3, tok: b.used_l3, className: "bg-med" },
  ];
  const l4 = b.used_l4 ?? 0;
  if (l4 > 0) {
    items.push({ layer: "L4", label: labels.L4, tok: l4, className: "bg-l4" });
  }
  return {
    items,
    total: Math.max(1, b.used_l1 + b.used_l2 + b.used_l3 + l4),
    dropped: b.dropped ?? [],
    estimated: false,
  };
}

// estimateBudget: rough fallback used only before the backend truth arrives (pre-first-byte streaming / old cache)
// L2 = patch byte count / 3 (same algorithm as the backend's estimateTokens); L1/L3 are empirical constants
function estimateBudget(_pr: PrMeta, files: File[], t: Dict): BudgetView {
  const labels = budgetLabels(t);
  let patchChars = 0;
  for (const f of files) patchChars += f.patch?.length ?? 0;
  const l2 = Math.max(200, Math.round(patchChars / 3));
  const l1 = 400 + files.length * 30;
  const l3 = Math.min(1600, Math.round((l1 + l2) / 12));
  return {
    items: [
      { layer: "L1", label: labels.L1, tok: l1, className: "bg-info" },
      { layer: "L2", label: labels.L2, tok: l2, className: "bg-ok" },
      { layer: "L3", label: labels.L3, tok: l3, className: "bg-med" },
    ],
    total: l1 + l2 + l3,
    dropped: [],
    estimated: true,
  };
}

function ContextDetail({ budget, t }: { budget: BudgetView; t: Dict }) {
  const hasL4 = budget.items.some((b) => b.layer === "L4");
  const ratio = hasL4 ? "L1:L2:L3:L4 ≈ 3:4:1:2" : "L1:L2:L3 ≈ 4:5:1";
  return (
    <ToolCard
      label={
        <>
          {t.agent.contextBudgetLabelPrefix}
          {ratio}
          {t.agent.contextBudgetTotalLabel}
          {(budget.total / 1000).toFixed(1)}K
          {budget.estimated ? <span className="text-faint">{t.agent.roughEstimateBadge}</span> : null}
        </>
      }
    >
      <div className="mb-2.5 flex h-2 overflow-hidden rounded-full">
        {budget.items.map((b) => (
          <span
            key={b.layer}
            className={b.className}
            style={{ width: `${(b.tok / budget.total) * 100}%` }}
          />
        ))}
      </div>
      <div className="flex flex-col gap-1.5">
        {budget.items.map((b) => (
          <div key={b.layer} className="flex items-center gap-2">
            <span className={cn("h-2 w-2 shrink-0 rounded-sm", b.className)} />
            <code className="w-5 font-mono text-[10.5px] font-semibold">{b.layer}</code>
            <span className="min-w-0 flex-1 truncate text-xs text-text-2">{b.label}</span>
            <span className="font-mono text-[10.5px] text-muted">
              {(b.tok / 1000).toFixed(2)}K
            </span>
          </div>
        ))}
      </div>
      <div className="mt-2.5 flex flex-wrap gap-1.5">
        {["README.md", "CONTRIBUTING.md", "CLAUDE.md"].map((f) => (
          <code
            key={f}
            className="rounded border border-med-bd bg-med-bg px-1.5 py-0.5 font-mono text-[10px] text-med"
          >
            {f}
          </code>
        ))}
      </div>
      {budget.dropped.length > 0 ? (
        <div className="mt-2 border-t border-dashed border-border pt-2 text-[10.5px] text-muted">
          <span className="font-medium text-high">{t.agent.overBudgetDroppedLabel}</span>
          <span className="ml-1.5 font-mono text-faint">
            {budget.dropped.slice(0, 3).join(" · ")}
            {budget.dropped.length > 3 ? ` …+${budget.dropped.length - 3}` : ""}
          </span>
        </div>
      ) : null}
    </ToolCard>
  );
}

function LlmDetail({
  summary,
  risks,
  suggestions,
  hasSummary,
  risksDone,
  suggestionsDone,
  streaming,
  t,
}: {
  summary: string;
  risks: Risk[];
  suggestions: Suggestion[];
  hasSummary: boolean;
  risksDone: boolean;
  suggestionsDone: boolean;
  streaming: boolean;
  t: Dict;
}) {
  // Three lanes: summary / risks / suggestions
  // Status: not yet done while running -> "running"; done -> "done"; not started upstream -> "pending"
  const stage = (done: boolean, started: boolean): StepStatus =>
    done ? "done" : started ? "running" : "pending";
  const summaryStage: StepStatus =
    summary && (risksDone || !streaming) ? "done" : hasSummary ? "running" : "pending";
  return (
    <ToolCard
      label={
        <>
          <Sparkle className="h-3 w-3" fill="currentColor" /> {t.agent.llmFanOutLabel}
        </>
      }
    >
      <Lane
        icon={<AlignLeft className="h-3 w-3" />}
        label={t.review.stageSummary}
        status={summaryStage}
        result={summaryStage === "done" ? t.agent.arrivedLabel : ""}
        t={t}
      />
      <Lane
        icon={<AlertTriangle className="h-3 w-3" />}
        label={t.review.risksPanelTitle}
        status={stage(risksDone, hasSummary)}
        result={risksDone ? t.agent.itemsCount(risks.length) : ""}
        t={t}
      />
      <Lane
        icon={<Sparkle className="h-3 w-3" />}
        label={t.agent.laneSuggestionsLabel}
        status={stage(suggestionsDone, risksDone)}
        result={suggestionsDone ? t.agent.suggestionsCountLabel(suggestions.length) : ""}
        t={t}
      />
      {summary ? (
        <div className="mt-2 max-h-[400px] overflow-y-auto border-t border-dashed border-border pt-2.5 pr-1 text-sm leading-relaxed text-text-2 [&_code]:rounded [&_code]:bg-surface-2 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[11px] [&_h1]:mt-2 [&_h1]:text-[13px] [&_h1]:font-semibold [&_h2]:mt-2 [&_h2]:text-xs [&_h2]:font-semibold [&_h3]:mt-2 [&_h3]:text-xs [&_h3]:font-semibold [&_li]:my-0.5 [&_p]:my-1.5 [&_strong]:font-medium [&_ul]:my-1 [&_ul]:list-disc [&_ul]:pl-4 [&_ol]:my-1 [&_ol]:list-decimal [&_ol]:pl-4">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{summary}</ReactMarkdown>
        </div>
      ) : null}
      {risksDone && risks.length > 0 ? (
        <div className="mt-1 flex flex-wrap gap-1.5">
          {risks.slice(0, 4).map((r, i) => (
            <span
              key={i}
              className="inline-flex items-center gap-1.5 rounded-full border border-border bg-surface px-2 py-0.5"
            >
              <SeverityBadge severity={r.severity} className="px-1.5 py-0 text-[10px]">
                {r.severity}
              </SeverityBadge>
              <code className="font-mono text-[10px] text-text-2">
                {r.file.split("/").pop()}
                {r.line ? `:${r.line}` : ""}
              </code>
            </span>
          ))}
        </div>
      ) : null}
    </ToolCard>
  );
}

function Lane({
  icon,
  label,
  status,
  result,
  t,
}: {
  icon: React.ReactNode;
  label: string;
  status: StepStatus;
  result?: string;
  t: Dict;
}) {
  return (
    <div className="flex items-center gap-2 py-[7px]">
      <span className="flex w-[18px] shrink-0 justify-center">
        {status === "running" ? (
          <Spinner size="xs" className="text-accent" />
        ) : status === "done" ? (
          <Check className="h-3.5 w-3.5 text-ok" strokeWidth={2.4} />
        ) : (
          <span className="inline-block h-2.5 w-2.5 rounded-full border-[1.5px] border-border-strong" />
        )}
      </span>
      <span className="text-muted">{icon}</span>
      <span
        className={cn(
          "text-sm font-medium",
          status === "pending" ? "text-muted" : "text-text",
        )}
      >
        {label}
      </span>
      <span className="ml-auto font-mono text-[10.5px] text-faint">
        {status === "done"
          ? result
          : status === "running"
            ? t.agent.streamingEllipsisLabel
            : t.agent.queuedLabel}
      </span>
    </div>
  );
}

function CacheDetail({ pr, t }: { pr: PrMeta; t: Dict }) {
  return (
    <ToolCard label={t.agent.cacheCardLabel}>
      <code className="font-mono text-xs text-text-2">
        key = <span className="text-info">{pr.owner}/{pr.repo}</span>:
        <span className="text-ok">{pr.pr}</span>:
        <span className="text-med">{pr.head_sha.slice(0, 11)}</span>
      </code>
      <div className="mt-1.5 text-xs text-muted">{t.agent.cacheHintNote}</div>
    </ToolCard>
  );
}

function FinalCard({
  pr: _pr,
  risks,
  suggestions,
  t,
}: {
  pr: PrMeta;
  risks: Risk[];
  suggestions: Suggestion[];
  t: Dict;
}) {
  return (
    <div className="pl-[38px]">
      <div className="mb-3 flex items-center gap-2 rounded-lg border border-border bg-surface px-3.5 py-2.5">
        <Check className="h-4 w-4 text-ok" strokeWidth={2.4} />
        <span className="text-sm font-semibold">{t.agent.reviewCompleteLabel}</span>
        <span className="text-xs text-muted">
          {t.review.stageSummary} + {t.agent.riskCountPhrase(risks.length)} +{" "}
          {t.agent.suggestionCountPhrase(suggestions.length)}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-2.5 font-mono text-[10.5px] text-faint">
        <span className="inline-flex items-center gap-1">
          <Sparkle className="h-[11px] w-[11px] text-accent" fill="currentColor" />
          DeepSeek · deepseek-chat <span className="text-med">(mock)</span>
        </span>
        <span>{t.agent.contextIncludesNote}</span>
      </div>
    </div>
  );
}

// SteerComposer: the bottom "steer" input bar; pure UI component — send / state machine lives in the parent AgentSessionView
// disabled = streaming mode (reviewId unknown); inFlight = parent is currently running a steer request
function SteerComposer({
  pr,
  disabled,
  inFlight,
  onSend,
  t,
}: {
  pr: PrMeta;
  disabled: boolean;
  inFlight: boolean;
  onSend: (text: string, stage: "risks" | "suggestions", mode: SteerMode) => void;
  t: Dict;
}) {
  const [text, setText] = useState("");
  const [stage, setStage] = useState<"risks" | "suggestions">("risks");
  const [mode, setMode] = useState<SteerMode>("stage");
  const githubURL = `https://github.com/${pr.owner}/${pr.repo}/pull/${pr.pr}`;
  const enabled = !disabled && !inFlight;

  function send() {
    const v = text.trim();
    if (!v || !enabled) return;
    onSend(v, stage, mode);
    setText("");
  }

  return (
    <div className="border-t border-border bg-surface px-6 py-3">
      <div className="mx-auto max-w-[860px]">
        <div className="mb-2 flex items-center gap-2">
          <span className="inline-flex items-center gap-1.5 rounded-md border border-border bg-surface-2 px-2.5 py-1 font-mono text-xs text-info">
            <GitBranch className="h-3 w-3" />
            {pr.head_ref || "head"}
            {pr.stats ? (
              <>
                <span className="text-ok">+{pr.stats.additions}</span>
                <span className="text-high">−{pr.stats.deletions}</span>
              </>
            ) : null}
          </span>
          {/* Mode switch: stage (rerun risks/suggestions) / agent (run the ReAct loop with tool calls) */}
          <div className="flex gap-[3px] rounded-md border border-border bg-surface-2 p-[2px]">
            {(["stage", "agent"] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                aria-pressed={mode === m}
                className={cn(
                  "rounded-sm px-2 py-[3px] font-mono text-[10.5px] transition-colors",
                  mode === m ? "bg-surface text-text" : "text-muted hover:text-text",
                )}
              >
                {m === "stage" ? t.agent.rerunStageLabel : t.agent.agentDeepDiveLabel}
              </button>
            ))}
          </div>
          {/* Stage picker: only shown when mode=stage; agent mode skips it (doesn't rerun a stage) */}
          {mode === "stage" ? (
            <div className="flex gap-[3px] rounded-md border border-border bg-surface-2 p-[2px]">
              {(["risks", "suggestions"] as const).map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setStage(s)}
                  aria-pressed={stage === s}
                  className={cn(
                    "rounded-sm px-2 py-[3px] font-mono text-[10.5px] transition-colors",
                    stage === s ? "bg-surface text-text" : "text-muted hover:text-text",
                  )}
                >
                  {t.agent.steerStageLabel(s)}
                </button>
              ))}
            </div>
          ) : null}
          <a
            href={githubURL}
            target="_blank"
            rel="noreferrer"
            className="ml-auto inline-flex h-7 items-center gap-1 rounded-md border border-border-strong bg-surface px-2.5 text-xs text-text-2 hover:bg-surface-hover hover:text-text"
          >
            <ExternalLink className="h-3 w-3" />
            {t.agent.viewPrLinkLabel}
          </a>
        </div>
        <div
          className={cn(
            "flex items-end gap-2 rounded-lg border bg-surface p-1.5 shadow-sm",
            enabled ? "border-border-strong" : "border-border",
          )}
        >
          <textarea
            value={text}
            onChange={(e) => setText(e.target.value)}
            disabled={!enabled}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
            }}
            rows={1}
            placeholder={
              disabled
                ? t.agent.steerDisabledPlaceholder
                : mode === "agent"
                  ? t.agent.steerAgentModePlaceholder
                  : t.agent.steerStageModePlaceholder(stage)
            }
            className="max-h-[120px] min-w-0 flex-1 resize-none border-none bg-transparent px-1.5 py-1.5 text-sm leading-snug text-text outline-none placeholder:text-faint disabled:cursor-not-allowed"
          />
          <button
            type="button"
            onClick={send}
            disabled={!enabled || !text.trim()}
            className="inline-flex h-[30px] items-center rounded-md bg-accent px-2.5 text-accent-fg hover:opacity-90 disabled:opacity-50"
          >
            {inFlight ? <Spinner size="xs" className="text-accent-fg" /> : <Send className="h-3.5 w-3.5" />}
          </button>
        </div>
      </div>
    </div>
  );
}
