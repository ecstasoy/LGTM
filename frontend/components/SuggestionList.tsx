"use client";

import type { Suggestion } from "@/lib/types";
import { useT } from "@/lib/i18n/context";

const typeClass = {
  bug: "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300",
  security: "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300",
  concurrency: "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300",
  perf: "bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300",
  style: "bg-zinc-100 text-zinc-700 dark:bg-zinc-800 dark:text-zinc-400",
} as const;

interface Props {
  suggestions: Suggestion[];
}

// SuggestionList 按 file 分组，每条 type 徽章 + title + body + 可选 patch 前后对比
export function SuggestionList({ suggestions }: Props) {
  const t = useT();
  if (suggestions.length === 0) return null;

  const byFile = new Map<string, Suggestion[]>();
  for (const s of suggestions) {
    const list = byFile.get(s.file) ?? [];
    list.push(s);
    byFile.set(s.file, list);
  }

  return (
    <section className="space-y-3">
      <h3 className="text-base font-medium">{t.review.inlineSuggestionsHeading(suggestions.length)}</h3>
      <div className="space-y-2">
        {[...byFile.entries()].map(([file, items]) => (
          <FileGroup key={file} file={file} suggestions={items} t={t} />
        ))}
      </div>
    </section>
  );
}

function FileGroup({
  file,
  suggestions,
  t,
}: {
  file: string;
  suggestions: Suggestion[];
  t: ReturnType<typeof useT>;
}) {
  return (
    <details open className="rounded-md border border-zinc-200 dark:border-zinc-800">
      <summary className="cursor-pointer bg-zinc-50 px-3 py-2 text-sm dark:bg-zinc-900/40">
        <code className="rounded bg-zinc-100 px-1 text-xs dark:bg-zinc-800">{file}</code>
        <span className="ml-2 text-xs text-zinc-500">{t.review.suggestionCountBadge(suggestions.length)}</span>
      </summary>
      <ul className="space-y-3 px-3 pb-3 pt-2">
        {suggestions.map((s, i) => (
          <SuggestionItem key={`${file}-${i}`} suggestion={s} />
        ))}
      </ul>
    </details>
  );
}

function SuggestionItem({ suggestion: s }: { suggestion: Suggestion }) {
  return (
    <li className="space-y-2 border-l-2 border-zinc-200 pl-3 dark:border-zinc-700">
      <div className="flex items-baseline gap-2">
        <span
          className={`shrink-0 rounded px-2 py-0.5 text-xs font-medium uppercase ${typeClass[s.type]}`}
        >
          {s.type}
        </span>
        <span className="text-xs text-zinc-500">line {s.line}</span>
        <span className="text-sm font-medium">{s.title}</span>
      </div>
      <p className="text-sm leading-relaxed text-zinc-700 dark:text-zinc-300">{s.body}</p>
      {s.patch ? <PatchPreview patch={s.patch} /> : null}
    </li>
  );
}

function PatchPreview({ patch }: { patch: NonNullable<Suggestion["patch"]> }) {
  return (
    <div className="mt-1 space-y-1 text-xs">
      <pre className="overflow-x-auto rounded border border-red-200 bg-red-50 p-2 text-red-900 dark:border-red-900/40 dark:bg-red-900/15 dark:text-red-200">
        <code>{patch.before}</code>
      </pre>
      <pre className="overflow-x-auto rounded border border-green-200 bg-green-50 p-2 text-green-900 dark:border-green-900/40 dark:bg-green-900/15 dark:text-green-200">
        <code>{patch.after}</code>
      </pre>
    </div>
  );
}
