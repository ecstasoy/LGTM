"use client";

import { useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  Check,
  PanelRight,
  Send,
  Sparkle,
  Wrench,
} from "lucide-react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { streamSteer } from "@/lib/sse";
import { cn } from "@/lib/utils";
import { Avatar } from "@/components/ui/avatar";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/context";
import type { Dict } from "@/lib/i18n/dictionaries/zh";

// chatProse 聊天气泡内 markdown 排版：紧凑间距 + 小字号；与 SummaryCard 的宽松排版区分
const chatProse =
  "[&_p]:my-1.5 [&_p:first-child]:mt-0 [&_p:last-child]:mb-0 [&_ul]:my-1.5 [&_ul]:list-disc [&_ul]:pl-4 [&_ol]:my-1.5 [&_ol]:list-decimal [&_ol]:pl-4 [&_li]:my-0.5 [&_h1]:my-1.5 [&_h1]:text-sm [&_h1]:font-semibold [&_h2]:my-1.5 [&_h2]:text-[13px] [&_h2]:font-semibold [&_h3]:my-1.5 [&_h3]:text-xs [&_h3]:font-semibold [&_code]:rounded [&_code]:bg-surface [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[10.5px] [&_pre]:my-2 [&_pre]:overflow-x-auto [&_pre]:rounded [&_pre]:bg-surface [&_pre]:p-2 [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_strong]:font-semibold";

interface Props {
  onClose: () => void;
  // cached 模式才有 reviewId；streaming / 首次评审时 undefined → 输入禁用
  reviewId?: string;
}

type MsgRole = "user" | "assistant" | "tool";

interface ToolMeta {
  id: string;
  name: string;
  arguments?: string;
  result?: string;
  status: "running" | "done" | "error";
}

interface Msg {
  role: MsgRole;
  text: string;
  cites?: string[];
  tool?: ToolMeta; // role=tool 时填
}

// AgentPanel 右侧 360px 抽屉 dock：追问这个 PR。
// 接 streamSteer mode=agent：每次提问跑 agent.Run loop；
// SSE tool_call_start/done → 在 chat 内插 tool message；info（"Agent 完成..."）→ assistant message。
export function AgentPanel({ onClose, reviewId }: Props) {
  const t = useT();
  // The greeting is NOT seeded into msgs: it's rendered straight from t.agent.introMessage below,
  // so it re-localizes immediately if the user toggles the language while the panel stays open.
  // Persisting it into state would freeze it in whatever locale was active at mount.
  const [msgs, setMsgs] = useState<Msg[]>([]);
  // Mutated on every render, read only when streamSteer's async path actually throws — so an
  // in-flight steer request picks up a locale change without needing to be restarted.
  const tRef = useRef(t);
  tRef.current = t;
  const [input, setInput] = useState("");
  const [thinking, setThinking] = useState(false);
  const inFlightRef = useRef(false);
  const scrollRef = useRef<HTMLDivElement>(null);
  const abortControllerRef = useRef<AbortController | null>(null);

  useEffect(() => {
    if (scrollRef.current) scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
  }, [msgs, thinking]);

  useEffect(() => {
    return () => {
      abortControllerRef.current?.abort();
    };
  }, []);

  async function send(text?: string) {
    const q = (text ?? input).trim();
    if (!q || !reviewId || inFlightRef.current) return;
    inFlightRef.current = true;
    setMsgs((m) => [...m, { role: "user", text: q }]);
    setInput("");
    setThinking(true);

    abortControllerRef.current?.abort();
    const controller = new AbortController();
    abortControllerRef.current = controller;

    try {
      await streamSteer(
        reviewId,
        q,
        "risks", // agent 模式忽略 stage 字段；填占位
        {
          onToolCallStart: (call) => {
            setMsgs((m) => {
              const idx = m.findIndex((msg) => msg.role === "tool" && msg.tool?.id === call.id);
              const nextMsg: Msg = {
                role: "tool",
                text: call.name,
                tool: {
                  id: call.id,
                  name: call.name,
                  arguments: call.arguments,
                  status: "running",
                },
              };
              if (idx >= 0) {
                const copy = [...m];
                copy[idx] = nextMsg;
                return copy;
              }
              return [...m, nextMsg];
            });
          },
          onToolCallDone: (call) => {
            setMsgs((m) => {
              let found = false;
              const status: ToolMeta["status"] = call.result?.startsWith("error:") ? "error" : "done";
              const next: Msg[] = m.map((msg) => {
                if (msg.role === "tool" && msg.tool?.id === call.id) {
                  found = true;
                  return {
                    ...msg,
                    tool: { ...msg.tool, result: call.result, status },
                  };
                }
                return msg;
              });
              if (found) return next;
              // 收到 done 但没对应 running 帧时，直接 push 一条终态消息
              const fallback: Msg = {
                role: "tool",
                text: call.name,
                tool: { id: call.id, name: call.name, result: call.result, status },
              };
              return [...next, fallback];
            });
          },
          onInfo: (info) => {
            // 后端两条 info：开头是 "Agent 启动..."（忽略）；结束是 "Agent 完成（N 步）：..."（assistant）
            const finishMatch = info.match(/^Agent 完成（\d+ 步）：(.*)$/s);
            if (finishMatch) {
              setMsgs((m) => [...m, { role: "assistant", text: finishMatch[1].trim() }]);
            }
          },
          onStageError: (_s, msg) => {
            setMsgs((m) => [...m, { role: "assistant", text: `❌ ${msg}` }]);
          },
        },
        tRef,
        controller.signal,
        "agent",
      );
    } catch (e) {
      setMsgs((m) => [
        ...m,
        { role: "assistant", text: `❌ ${e instanceof Error ? e.message : String(e)}` },
      ]);
    } finally {
      inFlightRef.current = false;
      setThinking(false);
    }
  }

  const chips = t.agent.suggestedChips;
  const enabled = !!reviewId && !thinking;
  const placeholder = reviewId ? t.agent.panelPlaceholderActive : t.agent.panelPlaceholderDisabled;

  function handleClose() {
    abortControllerRef.current?.abort();
    onClose();
  }

  return (
    <aside className="absolute inset-y-0 right-0 z-40 flex h-full w-full max-w-sm shrink-0 flex-col border-l border-border bg-surface lg:static lg:z-auto lg:w-[360px] lg:max-w-none">
      <header className="flex items-center gap-2 border-b border-border px-3 py-2.5">
        <span className="inline-flex h-[22px] w-[22px] items-center justify-center rounded-md bg-accent text-accent-fg">
          <Sparkle className="h-[13px] w-[13px]" fill="currentColor" />
        </span>
        <span className="text-sm font-semibold">{t.agent.panelTitle}</span>
        <span className="rounded-full border border-border bg-surface-2 px-1.5 py-px font-mono text-[10px] text-faint">
          agent
        </span>
        <button
          type="button"
          onClick={handleClose}
          className="ml-auto inline-flex h-7 w-7 items-center justify-center rounded-md text-muted hover:bg-surface-hover hover:text-text"
          aria-label={t.agent.closeAriaLabel}
        >
          <PanelRight className="h-4 w-4" />
        </button>
      </header>

      <div ref={scrollRef} className="flex-1 overflow-y-auto px-3.5 py-1.5">
        <AgentMessage msg={{ role: "assistant", text: t.agent.introMessage }} />
        {msgs.map((m, i) =>
          m.role === "tool" ? (
            <ToolMessage key={i} msg={m} t={t} />
          ) : (
            <AgentMessage key={i} msg={m} />
          ),
        )}
        {thinking ? (
          <div className="flex items-center gap-2 py-2.5">
            <span className="inline-flex h-6 w-6 items-center justify-center rounded-md bg-accent text-accent-fg">
              <Sparkle className="h-3.5 w-3.5" fill="currentColor" />
            </span>
            <span className="flex items-center gap-1">
              {[0, 1, 2].map((i) => (
                <span
                  key={i}
                  className="inline-block h-1.5 w-1.5 rounded-full bg-muted"
                  style={{ animation: `pulse-dot 1s ${i * 0.18}s infinite` }}
                />
              ))}
            </span>
          </div>
        ) : null}
      </div>

      <div className="flex flex-wrap gap-1.5 px-3 pt-2 pb-1.5">
        {chips.map((c) => (
          <button
            key={c}
            type="button"
            onClick={() => send(c)}
            disabled={!enabled}
            className="whitespace-nowrap rounded-full border border-border bg-surface-2 px-2.5 py-1 text-[11px] text-text-2 hover:bg-surface-hover hover:text-text disabled:opacity-50"
          >
            {c}
          </button>
        ))}
      </div>
      <div className="px-3 pb-3 pt-1">
        <div
          className={cn(
            "flex items-end gap-1.5 rounded-lg border bg-surface p-1.5",
            enabled ? "border-border-strong" : "border-border",
          )}
        >
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            disabled={!enabled}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
            }}
            placeholder={placeholder}
            rows={1}
            className="max-h-[120px] min-w-0 flex-1 resize-none border-none bg-transparent px-1.5 py-1.5 text-sm leading-snug text-text outline-none placeholder:text-faint disabled:cursor-not-allowed"
          />
          <button
            type="button"
            onClick={() => send()}
            disabled={!enabled || !input.trim()}
            className="inline-flex h-7 items-center rounded-md bg-accent px-2 text-accent-fg hover:opacity-90 disabled:opacity-50"
          >
            {thinking ? (
              <Spinner size="xs" className="text-accent-fg" />
            ) : (
              <Send className="h-3.5 w-3.5" />
            )}
          </button>
        </div>
        <div className="mt-1.5 text-center text-[10px] text-faint">
          {t.agent.contextFooterNote}
        </div>
      </div>
    </aside>
  );
}

function AgentMessage({ msg }: { msg: Msg }) {
  const isUser = msg.role === "user";
  return (
    <div
      className={cn(
        "animate-fade-up flex gap-2 py-2.5",
        isUser ? "flex-row-reverse" : "flex-row",
      )}
    >
      <span className="shrink-0">
        {isUser ? (
          <Avatar name="you" size="md" />
        ) : (
          <span className="inline-flex h-6 w-6 items-center justify-center rounded-md bg-accent text-accent-fg">
            <Sparkle className="h-3.5 w-3.5" fill="currentColor" />
          </span>
        )}
      </span>
      <div className="max-w-[82%]">
        <div
          className={cn(
            "rounded-lg px-3 py-2 text-xs leading-[1.6]",
            isUser
              ? "whitespace-pre-wrap bg-accent text-accent-fg"
              : cn("border border-border bg-surface-2 text-text", chatProse),
          )}
        >
          {isUser ? (
            msg.text
          ) : (
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.text}</ReactMarkdown>
          )}
        </div>
        {msg.cites ? (
          <div className="mt-1.5 flex flex-wrap gap-1">
            {msg.cites.map((c) => (
              <code
                key={c}
                className="rounded border border-border bg-surface-2 px-1.5 py-px font-mono text-[10px] text-info"
              >
                {c}
              </code>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

// ToolMessage shows an agent tool call as a low-contrast, single line — kept out of the way of the main conversation
function ToolMessage({ msg, t }: { msg: Msg; t: Dict }) {
  const tool = msg.tool;
  if (!tool) return null;
  const argsPreview = tool.arguments?.slice(0, 80) ?? "";
  const truncated = tool.arguments && tool.arguments.length > 80;
  return (
    <div className="flex gap-2 py-1.5">
      <span className="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-md border border-border bg-surface-2 text-muted">
        <Wrench className="h-3 w-3" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-1.5 text-[11px]">
          {tool.status === "running" ? (
            <Spinner size="xs" className="text-accent" />
          ) : tool.status === "error" ? (
            <AlertTriangle className="h-3 w-3 text-high" />
          ) : (
            <Check className="h-3 w-3 text-ok" strokeWidth={2.4} />
          )}
          <code className="font-mono font-semibold">{tool.name}</code>
          {argsPreview ? (
            <code className="min-w-0 truncate font-mono text-[10px] text-faint">
              {argsPreview}
              {truncated ? "…" : ""}
            </code>
          ) : null}
        </div>
        {tool.result && tool.status !== "running" ? (
          <details className="mt-0.5 text-[10px]">
            <summary className="cursor-pointer text-faint hover:text-text">
              {tool.status === "error" ? t.agent.viewErrorLabel : t.agent.viewResultLabel(tool.result.length)}
            </summary>
            <pre
              className={cn(
                "mt-1 max-h-[180px] overflow-y-auto whitespace-pre-wrap rounded border border-border bg-surface-2 p-1.5",
                tool.status === "error" ? "text-high" : "text-text-2",
              )}
            >
              {tool.result.slice(0, 800)}
              {tool.result.length > 800 ? t.agent.truncatedNote(tool.result.length - 800) : ""}
            </pre>
          </details>
        ) : null}
      </div>
    </div>
  );
}
