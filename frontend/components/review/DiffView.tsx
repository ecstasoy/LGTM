"use client";

import { FileCode2 } from "lucide-react";

import type { File, Risk, Suggestion } from "@/lib/types";
import { useT } from "@/lib/i18n/context";
import { FileDiff } from "./FileDiff";

interface Props {
  files: File[];
  risks?: Risk[];
  suggestions?: Suggestion[];
  expandedFilePath?: string;
  expandedFileNonce?: number;
  // 流式生成行内建议未完时，header 右侧显示 spinner（v1 默认 false）
  streamingSuggestions?: boolean;
}

// DiffView Diff 视图入口：汇总头 + 按文件渲染 FileDiff 卡。
// 把 risks/suggestions 按 (file, line) 聚合后传给每个 FileDiff。
// 严格对齐 design 原型 ReviewResult.jsx 的 diff view 区。
export function DiffView({
  files,
  risks,
  suggestions,
  expandedFilePath,
  expandedFileNonce,
}: Props) {
  const t = useT();
  if (files.length === 0) {
    return (
      <section className="rounded-lg border border-border bg-surface p-6 text-center text-sm text-muted">
        {t.diff.emptyState}
      </section>
    );
  }

  // 按 file → Map<line, severity> 索引 risks（同行多 risk 时保留首个 severity）
  const riskByFile = new Map<string, Map<number, Risk["severity"]>>();
  if (risks) {
    for (const r of risks) {
      if (typeof r.line !== "number") continue;
      let m = riskByFile.get(r.file);
      if (!m) {
        m = new Map();
        riskByFile.set(r.file, m);
      }
      if (!m.has(r.line)) m.set(r.line, r.severity);
    }
  }
  // 按 file → Map<line, Suggestion[]> 索引 suggestions
  const sugByFile = new Map<string, Map<number, Suggestion[]>>();
  if (suggestions) {
    for (const s of suggestions) {
      let m = sugByFile.get(s.file);
      if (!m) {
        m = new Map();
        sugByFile.set(s.file, m);
      }
      const list = m.get(s.line) ?? [];
      list.push(s);
      m.set(s.line, list);
    }
  }

  const totals = files.reduce(
    (acc, f) => ({ adds: acc.adds + f.additions, dels: acc.dels + f.deletions }),
    { adds: 0, dels: 0 },
  );

  return (
    <div className="flex flex-col gap-3">
      <header className="flex items-center gap-2">
        <FileCode2 className="h-[15px] w-[15px] text-muted" />
        <span className="text-sm font-semibold">{t.diff.header}</span>
        <span className="font-mono text-xs text-faint">
          {files.length} files · <span className="text-ok">+{totals.adds}</span>{" "}
          <span className="text-high">−{totals.dels}</span>
        </span>
      </header>
      {files.map((f) => (
        <FileDiff
          key={f.path}
          file={f}
          riskByLine={riskByFile.get(f.path)}
          suggestionsByLine={sugByFile.get(f.path)}
          expanded={expandedFilePath === f.path}
          expandedNonce={expandedFilePath === f.path ? expandedFileNonce : undefined}
        />
      ))}
    </div>
  );
}
