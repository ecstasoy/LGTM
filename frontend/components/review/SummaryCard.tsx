"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { AlignLeft } from "lucide-react";

import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/context";

interface Props {
  summary: string;
  streaming?: boolean;
}

// SummaryCard 变更总结卡：左上图标块 + 标题；streaming=true 右上挂"流式生成中" + Spinner，正文末尾闪烁光标
// 对齐 design 原型 SummaryCard 视觉（surface 卡 + accent 图标块）
export function SummaryCard({ summary, streaming = false }: Props) {
  const t = useT();
  if (!summary && !streaming) return null;
  return (
    <section className="rounded-lg border border-border bg-surface">
      <header className="flex items-center justify-between border-b border-border px-4 py-3">
        <div className="flex items-center gap-2.5">
          <span className="inline-flex h-7 w-7 items-center justify-center rounded-md bg-surface-2 text-accent">
            <AlignLeft className="h-4 w-4" />
          </span>
          <h2 className="text-sm font-semibold">{t.review.summaryCardTitle}</h2>
        </div>
        {streaming ? (
          <span className="inline-flex items-center gap-1.5 text-xs text-muted">
            <Spinner size="xs" />
            {t.review.summaryStreamingBadge}
          </span>
        ) : null}
      </header>
      <div className="space-y-3 px-4 py-4 text-sm leading-relaxed text-text [&_code]:rounded [&_code]:bg-surface-2 [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[12px] [&_h1]:mt-4 [&_h1]:text-base [&_h1]:font-semibold [&_h2]:mt-3 [&_h2]:text-sm [&_h2]:font-semibold [&_h3]:mt-3 [&_h3]:mb-1 [&_h3]:text-[13px] [&_h3]:font-semibold [&_li]:my-1 [&_ol]:my-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_p]:my-2 [&_strong]:font-semibold [&_strong]:text-text [&_ul]:my-2 [&_ul]:list-disc [&_ul]:pl-5">
        {summary ? (
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{summary}</ReactMarkdown>
        ) : (
          <p className="text-muted">{t.review.summaryGenerating}</p>
        )}
        {streaming && summary ? (
          <span className="inline-block h-3 w-[5px] animate-caret-blink bg-text align-middle" />
        ) : null}
      </div>
    </section>
  );
}
