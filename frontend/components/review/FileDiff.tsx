"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, ChevronDown, ChevronRight } from "lucide-react";

import type { File, Risk, Suggestion } from "@/lib/types";
import { cn } from "@/lib/utils";
import { parsePatch, type DiffLine as DiffLineModel } from "@/lib/parsePatch";
import { highlightHTML, langFromPath } from "@/lib/highlight";
import { FileStatusBadge } from "@/components/ui/file-status-badge";
import { useT } from "@/lib/i18n/context";
import { InlineSuggestion } from "./InlineSuggestion";

interface Props {
  file: File;
  // 该文件命中的 risks（按 line 索引）+ suggestions（按 line 索引）
  riskByLine?: Map<number, Risk["severity"]>;
  suggestionsByLine?: Map<number, Suggestion[]>;
  expanded?: boolean;
  expandedNonce?: number;
}

// FileDiff 单文件 unified diff 卡，严格对齐 design 原型：
// sticky 文件头（chevron + status + 路径 + adds/dels + 右侧 N 风险）+ hunks（@@ 缩进对齐代码列）
// + 4 列 grid 代码行 + 命中风险的左侧 3px severity 色条 + 锚定行内建议气泡
export function FileDiff({ file, riskByLine, suggestionsByLine, expanded, expandedNonce }: Props) {
  const t = useT();
  const [collapsed, setCollapsed] = useState(false);
  const hunks = parsePatch(file.patch);
  const riskCount = riskByLine?.size ?? 0;
  const lang = langFromPath(file.path);

  useEffect(() => {
    if (expanded) setCollapsed(false);
  }, [expanded, expandedNonce]);

  return (
    <article
      id={`file-${file.path}`}
      className="overflow-hidden rounded-lg border border-border bg-surface shadow-sm"
    >
      <header
        role="button"
        tabIndex={0}
        onClick={() => setCollapsed((c) => !c)}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setCollapsed((c) => !c);
          }
        }}
        className={cn(
          "sticky top-0 z-10 flex cursor-pointer items-center gap-2 bg-surface-2 px-3 py-2.5",
          collapsed ? "" : "border-b border-border",
        )}
      >
        {collapsed ? (
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted" />
        ) : (
          <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted" />
        )}
        <FileStatusBadge status={file.status} />
        <code className="min-w-0 truncate font-mono text-[12.5px] font-medium text-text" title={file.path}>
          {file.path}
        </code>
        <span className="ml-2 flex shrink-0 gap-1.5 font-mono text-xs">
          <span className="text-ok">+{file.additions}</span>
          <span className="text-high">−{file.deletions}</span>
        </span>
        {riskCount > 0 ? (
          <span className="ml-auto inline-flex items-center gap-1 whitespace-nowrap text-xs text-muted">
            <AlertTriangle className="h-3 w-3 text-high" />
            {t.diff.riskCountLabel(riskCount)}
          </span>
        ) : null}
      </header>

      {!collapsed
        ? hunks.length === 0
          ? (
            <p className="px-3 py-3 text-xs text-muted">{t.diff.noPatchNote}</p>
          ) : (
            hunks.map((h, i) => (
              <div key={i}>
                <div className="border-b border-border bg-surface-3 px-3 py-[3px] pl-[50px] font-mono text-xs text-info opacity-90">
                  {h.header}
                </div>
                {h.lines.map((line, j) => {
                  const newLine = line.new;
                  const sevHit =
                    newLine != null ? riskByLine?.get(newLine) : undefined;
                  const suggestions =
                    newLine != null ? suggestionsByLine?.get(newLine) : undefined;
                  const anchorId = newLine !== null ? `L-${file.path}-${newLine}` : undefined;
                  return (
                    <div key={`${i}-${j}`}>
                      <DiffRow line={line} sevHit={sevHit} anchorId={anchorId} lang={lang} />
                      {suggestions?.map((s, k) => (
                        <InlineSuggestion key={`s-${k}`} suggestion={s} />
                      ))}
                    </div>
                  );
                })}
              </div>
            ))
          )
        : null}
    </article>
  );
}

const sevBar: Record<Risk["severity"], string> = {
  high: "bg-high",
  medium: "bg-med",
  low: "bg-low",
};

function DiffRow({
  line,
  sevHit,
  anchorId,
  lang,
}: {
  line: DiffLineModel;
  sevHit?: Risk["severity"];
  anchorId?: string;
  lang: string;
}) {
  const t = useT();
  const isAdd = line.type === "add";
  const isDel = line.type === "del";
  const rowBg = isAdd ? "bg-add-bg" : isDel ? "bg-del-bg" : "";
  const gutterBg = isAdd ? "bg-add-gutter" : isDel ? "bg-del-gutter" : "";
  const sign = isAdd ? "+" : isDel ? "−" : " ";
  const signColor = isAdd ? "text-ok" : isDel ? "text-high" : "text-transparent";

  return (
    <div
      className={cn(
        "relative grid items-stretch leading-[20px] grid-cols-[32px_32px_14px_1fr] scroll-mt-20 sm:grid-cols-[44px_44px_18px_1fr]",
        rowBg,
      )}
      id={anchorId}
    >
      {sevHit ? (
        <span
          title={t.diff.riskMarkerTitle}
          className={cn("absolute inset-y-0 left-0 w-[3px]", sevBar[sevHit])}
        />
      ) : null}
      <span
        className={cn(
          "select-none border-r border-border px-2 text-right font-mono text-[11px] text-code-num",
          gutterBg,
        )}
      >
        {line.old ?? ""}
      </span>
      <span
        className={cn(
          "select-none border-r border-border px-2 text-right font-mono text-[11px] text-code-num",
          gutterBg,
        )}
      >
        {line.new ?? ""}
      </span>
      <span
        className={cn(
          "select-none px-1 text-center font-mono text-[12.5px] font-bold",
          signColor,
          gutterBg,
        )}
      >
        {sign}
      </span>
      <code
        className="hljs overflow-x-auto whitespace-pre-wrap break-words px-1.5 pr-3 font-mono text-[12.5px] text-text"
        dangerouslySetInnerHTML={{ __html: highlightHTML(line.text || " ", lang) }}
      />
    </div>
  );
}
