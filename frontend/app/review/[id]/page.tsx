"use client";

import { Suspense, use, useCallback, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { getReview, STAGES } from "@/lib/api";
import { streamReview } from "@/lib/sse";
import { friendlyError } from "@/lib/errors";
import type { BudgetReport, File, PrMeta, Risk, ReviewDetail, Suggestion } from "@/lib/types";
import { ReviewTopBar, type ViewKey } from "@/components/review/ReviewTopBar";
import { Sidebar } from "@/components/review/Sidebar";
import { SessionList } from "@/components/review/SessionList";
import { SummaryCard } from "@/components/review/SummaryCard";
import { RisksList } from "@/components/review/RisksList";
import { DiffView } from "@/components/review/DiffView";
import { AgentPanel } from "@/components/review/AgentPanel";
import { AdoptProvider } from "@/components/review/AdoptContext";
import { usePerms } from "@/lib/perms";
import {
  AgentSessionView,
  mergeToolDone,
  mergeToolStart,
  type ToolEvent,
} from "@/components/review/AgentSessionView";
import { Spinner } from "@/components/ui/spinner";
import type { StageState } from "@/components/review/StageChip";

interface PageProps {
  params: Promise<{ id: string }>;
}

type StageErrors = Partial<Record<"context" | "summary" | "risks" | "suggestions", string>>;

const VALID_VIEWS: ViewKey[] = ["report", "diff", "session"];

// /review/[id] 评审结果页。两种模式：
// - id === "streaming" + ?url= → 实时 SSE 流式（landing submit 后跳进来）
// - id 是 ULID → cached 模式，调 getReview 拉详情
// 两种模式共享同一套 UI（顶栏 / Sidebar / 主区 / Agent dock）；StageChips 在流式时按事件推进，
// 在 cached 模式全 done。
export default function ReviewDetailPage({ params }: PageProps) {
  const { id } = use(params);
  return (
    <Suspense fallback={<LoadingState />}>
      <ReviewDetailPageContent id={id} />
    </Suspense>
  );
}

function ReviewDetailPageContent({ id }: { id: string }) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const viewParam = searchParams.get("view") as ViewKey | null;
  const view: ViewKey =
    viewParam && VALID_VIEWS.includes(viewParam) ? viewParam : "report";

  const isStreaming = id === "streaming";
  const sourceURL = searchParams.get("url");
  const sourceModel = searchParams.get("model") ?? undefined; // L3：运行时选中的模型 key（应用到所有阶段）
  // L3 分阶段：m_summary / m_risks / m_suggestions 各阶段模型 key
  const stageModelEntries = STAGES.map(
    (s) => [s.key, searchParams.get(`m_${s.key}`)] as const,
  );
  const sourceStageModels = Object.fromEntries(
    stageModelEntries.filter(([, v]) => v),
  ) as Record<string, string>;
  const hasStageModels = Object.keys(sourceStageModels).length > 0;
  // effect 依赖用的稳定字符串（对象每次渲染都是新引用，不能直接进依赖数组）
  const stageModelDep = stageModelEntries.map(([, v]) => v ?? "").join(",");

  // 统一状态形状：cached 模式一次填齐，streaming 模式逐步填
  const [pr, setPr] = useState<PrMeta | null>(null);
  const [summary, setSummary] = useState("");
  const [risks, setRisks] = useState<Risk[]>([]);
  const [suggestions, setSuggestions] = useState<Suggestion[]>([]);
  const [files, setFiles] = useState<File[]>([]);
  const [budget, setBudget] = useState<BudgetReport | null>(null);
  const [toolEvents, setToolEvents] = useState<ToolEvent[]>([]);
  const [summaryDone, setSummaryDone] = useState(false);
  const [risksDone, setRisksDone] = useState(false);
  const [suggestionsDone, setSuggestionsDone] = useState(false);
  const [streaming, setStreaming] = useState(isStreaming);
  const [info, setInfo] = useState<string | null>(null);
  const [stageErrors, setStageErrors] = useState<StageErrors>({});
  const [error, setError] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  // retryNonce 递增即重跑取数 effect；流式失败 / 超时后「重试」按钮用
  const [retryNonce, setRetryNonce] = useState(0);

  const [agentOpen, setAgentOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [activeFile, setActiveFile] = useState<string | undefined>(undefined);
  const [expandRequest, setExpandRequest] = useState<{ path: string; nonce: number } | null>(null);
  // 流式跑完后后端通过 SSE review_id 帧回传 ULID；cached 模式直接用 URL 里的 id
  // 见 backend/internal/api/review.go persistReview 返值 + writeSSE("review_id", ...)
  const [streamedReviewID, setStreamedReviewID] = useState<string | null>(null);
  const effectiveReviewID = isStreaming ? streamedReviewID : id;

  // perms：用 PR meta 查当前用户对 base repo 的权限；驱动 InlineSuggestion 的「评论 / 提交」按钮
  // pr 未到位时 owner/repo 是 undefined → usePerms skip fetch
  const { perms, loading: permsLoading } = usePerms(pr?.owner, pr?.repo);

  const scrollRef = useRef<HTMLElement>(null);
  const pendingScroll = useRef<string | null>(null);

  // 移动端（< lg）默认收起侧栏，让主区占满；桌面保持展开。仅 mount 时判定一次。
  useEffect(() => {
    if (window.matchMedia("(max-width: 1023px)").matches) {
      setSidebarCollapsed(true);
    }
  }, []);

  // 拉数据：cached 模式 getReview；streaming 模式 streamReview
  useEffect(() => {
    let cancelled = false;
    let controller: AbortController | null = null;
    setToolEvents([]);
    if (isStreaming) {
      if (!sourceURL) {
        setError("缺少 url 参数");
        setLoaded(true);
        return;
      }
      setSummaryDone(false);
      controller = new AbortController();
      streamReview(sourceURL, {
        onPr: (p) => !cancelled && (setPr(p), setLoaded(true)),
        onFiles: (f) => !cancelled && setFiles(f),
        onBudgetReport: (b) => !cancelled && setBudget(b),
        onSummaryDelta: (d) => !cancelled && setSummary((s) => s + d),
        onRisksDone: (r) => {
          if (cancelled) return;
          setRisks(r);
          setRisksDone(true);
        },
        onSuggestionsDone: (s) => {
          if (cancelled) return;
          setSuggestions(s);
          setSuggestionsDone(true);
        },
        onReviewID: (rid) => !cancelled && setStreamedReviewID(rid),
        onToolCallStart: (call) =>
          !cancelled && setToolEvents((prev) => mergeToolStart(prev, call)),
        onToolCallDone: (call) =>
          !cancelled && setToolEvents((prev) => mergeToolDone(prev, call)),
        onInfo: (m) => !cancelled && setInfo(m),
        onStageError: (stage, msg) => {
          if (cancelled) return;
          if (stage === "summary") setSummaryDone(true);
          setStageErrors((prev) => ({ ...prev, [stage]: msg }));
        },
        onStageDone: (stage) => {
          if (cancelled || stage !== "summary") return;
          setSummaryDone(true);
        },
        onDone: () => !cancelled && (setSummaryDone(true), setStreaming(false)),
      }, controller.signal, sourceModel, hasStageModels ? sourceStageModels : undefined)
        .catch((e) => {
          if (e instanceof DOMException && e.name === "AbortError") return;
          if (!cancelled) setError(e instanceof Error ? e.message : String(e));
        })
        .finally(() => {
          if (!cancelled) {
            setStreaming(false);
            setLoaded(true);
          }
        });
    } else {
      // cached
      getReview(id)
        .then((d) => {
          if (cancelled) return;
          hydrateFromDetail(d, {
            setPr,
            setSummary,
            setRisks,
            setSuggestions,
            setFiles,
            setBudget,
            setSummaryDone,
            setRisksDone,
            setSuggestionsDone,
            setStreaming,
          });
          setLoaded(true);
        })
        .catch((e) => {
          if (!cancelled) {
            setError(e instanceof Error ? e.message : String(e));
            setLoaded(true);
          }
        });
    }
    return () => {
      cancelled = true;
      controller?.abort();
    };
  }, [id, isStreaming, sourceURL, sourceModel, stageModelDep, retryNonce]);

  // 重试：清掉错误与上一轮部分结果，bump retryNonce 触发取数 effect 重跑
  const retry = useCallback(() => {
    setError(null);
    setLoaded(false);
    setStreaming(isStreaming);
    setSummary("");
    setRisks([]);
    setSuggestions([]);
    setFiles([]);
    setBudget(null);
    setInfo(null);
    setStageErrors({});
    setSummaryDone(false);
    setRisksDone(false);
    setSuggestionsDone(false);
    setRetryNonce((n) => n + 1);
  }, [isStreaming]);

  // stage 状态机：基于 summary 是否有内容 / risksDone / suggestionsDone / streaming
  const stageStates = useMemo<{
    summary: StageState;
    risks: StageState;
    suggestions: StageState;
  }>(() => {
    if (!streaming) {
      // 流式结束 / cached → 全部按 done 显示
      return { summary: "done", risks: "done", suggestions: "done" };
    }
    const hasSummary = summary.length > 0;
    return {
      summary: summaryDone ? "done" : hasSummary ? "active" : "pending",
      risks: risksDone ? "done" : hasSummary ? "active" : "pending",
      suggestions: suggestionsDone ? "done" : risksDone ? "active" : "pending",
    };
  }, [streaming, summary, summaryDone, risksDone, suggestionsDone]);

  // scrollTop 直接赋值定位锚点 + 1.1s 闪烁高亮（design README §6 要求）
  const scrollToAnchor = useCallback((anchorId: string) => {
    const cont = scrollRef.current;
    const el = document.getElementById(anchorId);
    if (!cont || !el) return false;
    const top =
      el.getBoundingClientRect().top - cont.getBoundingClientRect().top + cont.scrollTop - 90;
    cont.scrollTop = Math.max(0, top);
    const prevBg = el.style.background;
    el.style.transition = "background .2s";
    el.style.background = "var(--accent-soft)";
    window.setTimeout(() => {
      el.style.background = prevBg;
    }, 1100);
    return true;
  }, []);

  useEffect(() => {
    if (view !== "diff" || !pendingScroll.current) return;
    const anchorId = pendingScroll.current;
    let cancelled = false;
    let attempts = 0;
    const flush = () => {
      if (cancelled) return;
      if (scrollToAnchor(anchorId)) {
        pendingScroll.current = null;
        return;
      }
      attempts += 1;
      if (attempts < 8) requestAnimationFrame(flush);
    };
    requestAnimationFrame(flush);
    return () => {
      cancelled = true;
    };
  }, [expandRequest?.nonce, view, scrollToAnchor]);

  function gotoView(next: ViewKey) {
    const params = new URLSearchParams(searchParams.toString());
    params.set("view", next);
    router.replace(`${pathname}?${params.toString()}`, { scroll: false });
  }

  function queueDiffJump(path: string, anchor: string) {
    setActiveFile(path);
    setExpandRequest((prev) => ({ path, nonce: (prev?.nonce ?? 0) + 1 }));
    pendingScroll.current = anchor;
    if (view !== "diff") gotoView("diff");
  }

  function pickFile(path: string) {
    queueDiffJump(path, `file-${path}`);
  }

  function pickRisk(r: Risk) {
    if (r.line == null) return;
    queueDiffJump(r.file, `L-${r.file}-${r.line}`);
  }

  if (error) {
    return (
      <section className="space-y-4 px-6 py-8">
        <Link href="/history" className="text-xs text-muted hover:text-text">
          ← 返回历史
        </Link>
        <p className="text-sm text-fail">{friendlyError(error)}</p>
        <button
          type="button"
          onClick={retry}
          className="rounded-md border border-border bg-surface px-3 py-1.5 text-sm text-text transition-colors hover:bg-surface-hover"
        >
          重试
        </button>
      </section>
    );
  }

  // streaming 模式：pr 事件未到时显加载占位，但顶栏不渲染（无 pr meta）
  if (!loaded || !pr) {
    return <LoadingState />;
  }

  return (
    <AdoptProvider
      reviewId={effectiveReviewID ?? undefined}
      prMeta={pr ?? undefined}
      perms={perms}
      permsLoading={permsLoading}
      suggestions={suggestions}
    >
    <div className="flex h-screen flex-col bg-bg">
      <ReviewTopBar
        pr={pr}
        view={view}
        stageStates={stageStates}
        onSidebarToggle={() => setSidebarCollapsed((c) => !c)}
        onToggleAgent={() => setAgentOpen((o) => !o)}
        agentOpen={agentOpen}
      />
      <div className="relative flex min-h-0 flex-1">
        {sidebarCollapsed ? null : (
          <>
            {/* 移动端：半透明遮罩，点击收起抽屉式侧栏 */}
            <button
              type="button"
              aria-label="关闭侧栏"
              onClick={() => setSidebarCollapsed(true)}
              className="absolute inset-0 z-30 bg-black/40 lg:hidden"
            />
            {view === "session" ? (
              <SessionList activeId={isStreaming ? undefined : id} />
            ) : (
              <Sidebar
                pr={pr}
                files={files}
                risks={risks}
                activeFile={activeFile}
                onPickFile={pickFile}
                onPickRisk={pickRisk}
              />
            )}
          </>
        )}
        <main ref={scrollRef} className="min-w-0 flex-1 overflow-y-auto">
          <div className="mx-auto flex max-w-[1080px] flex-col gap-4 px-5 py-5">
            {info ? <InfoBanner info={info} /> : null}
            {stageErrors.context ? (
              <StageErrorBanner stage="上下文" message={stageErrors.context} />
            ) : null}
            {stageErrors.suggestions ? (
              <StageErrorBanner stage="建议" message={stageErrors.suggestions} />
            ) : null}
            {view === "report" ? (
              <ReportContent
                summary={summary}
                summaryDone={summaryDone}
                risks={risks}
                risksDone={risksDone}
                streaming={streaming}
                stageErrors={stageErrors}
                budget={budget}
                onPickRisk={pickRisk}
              />
            ) : view === "diff" ? (
              <DiffView
                files={files}
                risks={risks}
                suggestions={suggestions}
                expandedFilePath={expandRequest?.path}
                expandedFileNonce={expandRequest?.nonce}
              />
            ) : (
              <AgentSessionView
                pr={pr}
                files={files}
                risks={risks}
                suggestions={suggestions}
                summary={summary}
                budget={budget}
                hasFiles={files.length > 0}
                risksDone={risksDone}
                suggestionsDone={suggestionsDone}
                streaming={streaming}
                reviewId={effectiveReviewID ?? undefined}
                onSteeredRisks={setRisks}
                onSteeredSuggestions={setSuggestions}
                onSteerToolCallStart={(call) => setToolEvents((prev) => mergeToolStart(prev, call))}
                onSteerToolCallDone={(call) => setToolEvents((prev) => mergeToolDone(prev, call))}
                onSteerInfo={setInfo}
                toolEvents={toolEvents}
              />
            )}
          </div>
        </main>
        {agentOpen ? (
          <>
            {/* 移动端：半透明遮罩，点击收起抽屉式追问面板 */}
            <button
              type="button"
              aria-label="关闭追问面板"
              onClick={() => setAgentOpen(false)}
              className="absolute inset-0 z-30 bg-black/40 lg:hidden"
            />
            <AgentPanel
              onClose={() => setAgentOpen(false)}
              reviewId={effectiveReviewID ?? undefined}
            />
          </>
        ) : null}
      </div>
    </div>
    </AdoptProvider>
  );
}

interface HydrateSetters {
  setPr: (pr: PrMeta) => void;
  setSummary: (s: string) => void;
  setRisks: (r: Risk[]) => void;
  setSuggestions: (s: Suggestion[]) => void;
  setFiles: (f: File[]) => void;
  setBudget: (b: BudgetReport | null) => void;
  setSummaryDone: (b: boolean) => void;
  setRisksDone: (b: boolean) => void;
  setSuggestionsDone: (b: boolean) => void;
  setStreaming: (b: boolean) => void;
}

// hydrateFromDetail 把 cached detail 一次填齐到所有 state
function hydrateFromDetail(d: ReviewDetail, h: HydrateSetters) {
  h.setPr({
    id: d.id,
    owner: d.owner,
    repo: d.repo,
    pr: d.pr,
    url: `https://github.com/${d.owner}/${d.repo}/pull/${d.pr}`,
    head_sha: d.head_sha,
    title: d.title ?? "",
    author: d.author,
    author_role: d.author_role,
    state: d.state,
    labels: d.labels,
    base_ref: d.base_ref,
    head_ref: d.head_ref,
    pr_created_at: d.pr_created_at,
    stats: d.stats,
    ci: d.ci,
    checks: d.checks,
    source: d.source,
  });
  h.setSummary(d.summary ?? "");
  h.setRisks(d.risks ?? []);
  h.setSuggestions(d.suggestions ?? []);
  h.setFiles(d.files ?? []);
  h.setBudget(d.budget_report ?? null);
  h.setSummaryDone(true);
  h.setRisksDone(true);
  h.setSuggestionsDone(true);
  h.setStreaming(false);
}

function ReportContent({
  summary,
  summaryDone,
  risks,
  risksDone,
  streaming,
  stageErrors,
  budget,
  onPickRisk,
}: {
  summary: string;
  summaryDone: boolean;
  risks: Risk[];
  risksDone: boolean;
  streaming: boolean;
  stageErrors: StageErrors;
  budget: BudgetReport | null;
  onPickRisk: (r: Risk) => void;
}) {
  const dropped = budget?.dropped ?? [];
  return (
    <>
      {dropped.length > 0 ? <DroppedFilesNotice files={dropped} /> : null}
      {stageErrors.summary ? (
        <StageErrorBanner stage="总结" message={stageErrors.summary} />
      ) : (
        <SummaryCard summary={summary} streaming={streaming && !summaryDone} />
      )}
      {stageErrors.risks ? (
        <StageErrorBanner stage="风险" message={stageErrors.risks} />
      ) : risks.length > 0 ? (
        <RisksList risks={risks} onPickRisk={onPickRisk} />
      ) : risksDone ? (
        <p className="text-sm text-muted">未发现风险。</p>
      ) : streaming ? (
        <p className="text-sm text-faint">扫描风险中…</p>
      ) : null}
    </>
  );
}

// DroppedFilesNotice 大 PR 超 token 预算丢文件时，在报告顶部声明评审范围不完整
function DroppedFilesNotice({ files }: { files: string[] }) {
  const shown = files.slice(0, 8);
  const rest = files.length - shown.length;
  return (
    <div className="rounded-md border border-high-bd bg-high-bg px-3 py-2 text-sm text-high">
      <div className="font-medium">
        {files.length} 个文件因体积超出预算，未纳入本次评审
      </div>
      <div className="mt-1 break-all font-mono text-[12px] text-text-2">
        {shown.join(" · ")}
        {rest > 0 ? ` …+${rest} 个` : ""}
      </div>
      <div className="mt-1 text-[12px] text-muted">摘要、风险与建议均不覆盖以上文件。</div>
    </div>
  );
}

function StageErrorBanner({ stage, message }: { stage: string; message: string }) {
  return (
    <div className="rounded-md border border-high-bd bg-high-bg px-3 py-2 text-sm text-high">
      <span className="font-medium">{stage}失败：</span>
      {message}
    </div>
  );
}

// agentReplyPrefix 匹配后端 SSE info 帧的 Agent 完成模板
// 命中即按 markdown 渲染 body（追问场景 LLM 输出含 ## / ``` / * 等格式）
const agentReplyPrefix = /^Agent 完成（\d+ 步）：(.*)$/s;

// infoProse Agent 回复 markdown 排版：与正文 text-sm 对齐；比 SummaryCard 更紧凑
const infoProse =
  "[&_p]:my-1.5 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_ul]:my-1.5 [&_ul]:list-disc [&_ul]:pl-5 [&_ol]:my-1.5 [&_ol]:list-decimal [&_ol]:pl-5 [&_li]:my-0.5 [&_h1]:mt-3 [&_h1]:mb-1.5 [&_h1]:text-base [&_h1]:font-semibold [&_h2]:mt-3 [&_h2]:mb-1.5 [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:mt-2 [&_h3]:mb-1 [&_h3]:text-[13px] [&_h3]:font-semibold [&_code]:rounded [&_code]:bg-surface [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[12px] [&_pre]:my-2 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-surface [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_strong]:font-semibold [&_strong]:text-text";

function InfoBanner({ info }: { info: string }) {
  const m = info.match(agentReplyPrefix);
  if (!m) {
    return (
      <div className="rounded-md border border-border bg-surface-2 px-4 py-3 text-sm text-text-2">
        {info}
      </div>
    );
  }
  const body = m[1].trim();
  return (
    <div className="rounded-md border border-border bg-surface-2 px-4 py-3 text-sm text-text-2">
      <div className="mb-2 text-xs text-muted">Agent 回复</div>
      <div className={infoProse}>
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{body}</ReactMarkdown>
      </div>
    </div>
  );
}

function LoadingState() {
  return (
    <p className="flex items-center gap-2 px-6 py-8 text-sm text-muted">
      <Spinner size="xs" /> 加载中…
    </p>
  );
}

