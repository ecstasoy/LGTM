"use client";

import { useMemo, useState } from "react";
import {
  AlertTriangle,
  ArrowRight,
  ChevronDown,
  ChevronRight,
  Folder,
  GitBranch,
  FileText,
} from "lucide-react";

import type { Check, File, PrMeta, Risk } from "@/lib/types";
import { cn } from "@/lib/utils";
import { formatAuthorRole } from "@/lib/format";
import { CIStatus, type CIStatusValue } from "@/components/ui/ci-status";
import { FileStatusBadge } from "@/components/ui/file-status-badge";
import { Avatar } from "@/components/ui/avatar";
import { SeverityBadge, CategoryBadge, type Category } from "@/components/ui/badge";
import { useT } from "@/lib/i18n/context";

interface Props {
  pr: PrMeta;
  files: File[];
  risks: Risk[];
  activeFile?: string;
  onPickFile?: (path: string) => void;
  onPickRisk?: (risk: Risk) => void;
}

type TabKey = "files" | "risks" | "info";

// Sidebar 评审页左侧 256px 栏，3 Tab：文件树 / 风险 / 信息。
// 严格对齐 design 原型 Sidebar.jsx 的 markup 结构。
export function Sidebar({ pr, files, risks, activeFile, onPickFile, onPickRisk }: Props) {
  const t = useT();
  const [tab, setTab] = useState<TabKey>("files");

  const tabs: Array<{ key: TabKey; label: string; icon: typeof FileText; count: number | null }> = [
    { key: "files", label: t.review.filesTabLabel, icon: FileText, count: files.length },
    { key: "risks", label: t.review.stageRisks, icon: AlertTriangle, count: risks.length },
    { key: "info", label: t.review.infoTabLabel, icon: GitBranch, count: null },
  ];

  return (
    <aside className="absolute inset-y-0 left-0 z-40 flex w-[84%] max-w-xs flex-col overflow-hidden border-r border-border bg-surface lg:static lg:z-auto lg:w-64 lg:max-w-none lg:flex-shrink-0">
      <div className="flex gap-0.5 border-b border-border p-1">
        {tabs.map(({ key, label, icon: Icon, count }) => {
          const active = tab === key;
          return (
            <button
              key={key}
              type="button"
              onClick={() => setTab(key)}
              className={cn(
                "flex flex-1 items-center justify-center gap-1.5 rounded-sm px-1 py-1.5 text-xs font-medium transition-colors",
                active
                  ? "bg-surface-hover text-text"
                  : "text-muted hover:bg-surface-hover hover:text-text",
              )}
            >
              <Icon className="h-3.5 w-3.5" />
              {label}
              {count !== null ? (
                <span className="font-mono text-[10px] text-faint">{count}</span>
              ) : null}
            </button>
          );
        })}
      </div>
      <div className="flex-1 overflow-y-auto">
        {tab === "files" ? (
          <FilesTab files={files} activeFile={activeFile} onPick={onPickFile} />
        ) : null}
        {tab === "risks" ? <RisksTab risks={risks} onPick={onPickRisk} /> : null}
        {tab === "info" ? <InfoTab pr={pr} /> : null}
      </div>
    </aside>
  );
}

// ─────────────── Files tab (tree) ───────────────

interface TreeNode {
  name: string;
  dir: boolean;
  children?: Record<string, TreeNode>;
  path: string;
  file?: File;
}

function buildTree(files: File[]): TreeNode {
  const root: TreeNode = { name: "", dir: true, children: {}, path: "" };
  for (const f of files) {
    const parts = f.path.split("/");
    let node = root;
    parts.forEach((p, i) => {
      const isLeaf = i === parts.length - 1;
      if (isLeaf) {
        node.children![p] = { name: p, dir: false, file: f, path: f.path };
      } else {
        if (!node.children![p]) {
          node.children![p] = {
            name: p,
            dir: true,
            children: {},
            path: parts.slice(0, i + 1).join("/"),
          };
        }
        node = node.children![p];
      }
    });
  }
  return root;
}

function FilesTab({
  files,
  activeFile,
  onPick,
}: {
  files: File[];
  activeFile?: string;
  onPick?: (path: string) => void;
}) {
  const t = useT();
  const tree = useMemo(() => buildTree(files), [files]);
  if (files.length === 0) {
    return <p className="px-3 py-3 text-xs text-muted">{t.review.noFileChanges}</p>;
  }
  return (
    <div className="px-1.5 py-1.5">
      <TreeNodeView node={tree} depth={0} activeFile={activeFile} onPick={onPick} />
    </div>
  );
}

function TreeNodeView({
  node,
  depth,
  activeFile,
  onPick,
}: {
  node: TreeNode;
  depth: number;
  activeFile?: string;
  onPick?: (path: string) => void;
}) {
  const [open, setOpen] = useState(true);

  if (!node.dir) {
    const f = node.file!;
    const active = activeFile === f.path;
    const indent = depth * 12 + 8;
    return (
      <button
        type="button"
        onClick={() => onPick?.(f.path)}
        className={cn(
          "flex w-full items-center gap-1.5 rounded-sm py-[3px] pr-2 text-left",
          active ? "bg-surface-hover" : "hover:bg-surface-hover",
        )}
        style={{ paddingLeft: indent }}
      >
        <FileStatusBadge status={f.status} />
        <span className="min-w-0 flex-1 truncate font-mono text-xs text-text" title={f.path}>
          {node.name}
        </span>
        <span className="flex shrink-0 gap-1 font-mono text-[10px]">
          <span className="text-ok">+{f.additions}</span>
          <span className="text-high">−{f.deletions}</span>
        </span>
      </button>
    );
  }

  // dir
  const kids = Object.values(node.children ?? {}).sort((a, b) =>
    a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1,
  );
  const indent = (depth - 1) * 12 + 8;
  return (
    <div>
      {node.name ? (
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          className="flex w-full items-center gap-1 rounded-sm py-[3px] pr-2 text-left font-mono text-xs text-muted hover:bg-surface-hover"
          style={{ paddingLeft: indent }}
        >
          {open ? (
            <ChevronDown className="h-3 w-3 shrink-0" />
          ) : (
            <ChevronRight className="h-3 w-3 shrink-0" />
          )}
          <Folder className="h-[13px] w-[13px] shrink-0" />
          <span className="truncate">{node.name}</span>
        </button>
      ) : null}
      {open
        ? kids.map((k) => (
            <TreeNodeView
              key={k.path || k.name}
              node={k}
              depth={depth + 1}
              activeFile={activeFile}
              onPick={onPick}
            />
          ))
        : null}
    </div>
  );
}

// ─────────────── Risks tab ───────────────

const SEV_RANK: Record<Risk["severity"], number> = { high: 0, medium: 1, low: 2 };

function RisksTab({ risks, onPick }: { risks: Risk[]; onPick?: (r: Risk) => void }) {
  const t = useT();
  if (risks.length === 0) {
    return <p className="px-3 py-3 text-xs text-muted">{t.review.sidebarNoRisksFound}</p>;
  }
  const sorted = [...risks].sort((a, b) => {
    const sevDiff = SEV_RANK[a.severity] - SEV_RANK[b.severity];
    return sevDiff !== 0 ? sevDiff : b.confidence - a.confidence;
  });
  const counts: Record<string, number> = {};
  for (const r of risks) counts[r.severity] = (counts[r.severity] ?? 0) + 1;

  return (
    <div>
      <div className="flex flex-wrap gap-1.5 px-3 pt-2.5 pb-1.5">
        {(["high", "medium", "low"] as const).map((s) =>
          counts[s] ? (
            <span key={s} className="inline-flex items-center gap-1">
              <SeverityBadge severity={s} className="px-1.5 py-0">
                {s}
              </SeverityBadge>
              <span className="font-mono text-[11px] text-muted">{counts[s]}</span>
            </span>
          ) : null,
        )}
      </div>
      <div className="flex flex-col gap-1 px-2 pb-2">
        {sorted.map((r, i) => (
          <button
            key={i}
            type="button"
            onClick={() => onPick?.(r)}
            className="block w-full rounded-md border border-border bg-surface px-2.5 py-2 text-left hover:bg-surface-hover"
          >
            <div className="mb-1 flex items-center gap-1.5">
              <SeverityBadge severity={r.severity} className="px-1.5 py-0">
                {r.severity}
              </SeverityBadge>
              <CategoryBadge category={r.category as Category} />
              <span className="ml-auto font-mono text-[10px] text-faint">
                {(r.confidence * 100).toFixed(0)}%
              </span>
            </div>
            <code className="mb-1 block truncate font-mono text-[11px] text-text-2">
              {r.file.split("/").pop()}
              {r.line ? `:${r.line}` : ""}
            </code>
            <p className="line-clamp-2 text-[11px] leading-snug text-muted">{r.reason}</p>
          </button>
        ))}
      </div>
    </div>
  );
}

// ─────────────── Info tab ───────────────

function InfoTab({ pr }: { pr: PrMeta }) {
  const t = useT();
  return (
    <div className="px-3.5 py-2.5">
      {pr.author ? (
        <InfoRow label={t.review.infoRowAuthor}>
          <span className="inline-flex items-center gap-1.5">
            <Avatar
              name={pr.author}
              src={`https://github.com/${pr.author}.png?size=36`}
              size="sm"
            />
            <span className="font-mono text-text-2">{pr.author}</span>
            {pr.author_role ? (
              <span className="text-[10px] text-faint">{formatAuthorRole(pr.author_role, t)}</span>
            ) : null}
          </span>
        </InfoRow>
      ) : null}
      {pr.head_ref || pr.base_ref ? (
        <InfoRow label={t.review.infoRowBranch}>
          <span className="inline-flex flex-wrap items-center gap-1 font-mono text-[11px]">
            {pr.head_ref ? (
              <code className="rounded bg-surface-2 px-1.5 py-[1px]">{pr.head_ref}</code>
            ) : null}
            {pr.head_ref && pr.base_ref ? <ArrowRight className="h-3 w-3 text-muted" /> : null}
            {pr.base_ref ? (
              <code className="rounded bg-surface-2 px-1.5 py-[1px]">{pr.base_ref}</code>
            ) : null}
          </span>
        </InfoRow>
      ) : null}
      <InfoRow label={t.review.infoRowCommit}>
        <code className="font-mono text-[11px]">{pr.head_sha?.slice(0, 11)}</code>
        {pr.stats ? (
          <span className="ml-2 text-faint">· {pr.stats.commits} commits</span>
        ) : null}
      </InfoRow>
      {pr.labels && pr.labels.length > 0 ? (
        <InfoRow label={t.review.infoRowLabels}>
          <span className="flex flex-wrap gap-1">
            {pr.labels.map((l) => (
              <span
                key={l}
                className="rounded-full border border-border bg-surface-2 px-2 py-0.5 font-mono text-[10px] text-muted"
              >
                {l}
              </span>
            ))}
          </span>
        </InfoRow>
      ) : null}

      {pr.checks && pr.checks.length > 0 ? (
        <>
          <div className="mt-3.5 mb-2 text-[10px] font-semibold uppercase tracking-wider text-muted">
            CI Checks
          </div>
          <div className="flex flex-col gap-0.5">
            {pr.checks.map((c, i) => (
              <CheckRow key={i} check={c} />
            ))}
          </div>
        </>
      ) : null}
    </div>
  );
}

function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex gap-2.5 border-b border-border py-[7px]">
      <span className="w-14 shrink-0 text-[11px] text-muted">{label}</span>
      <span className="min-w-0 flex-1 text-xs text-text-2">{children}</span>
    </div>
  );
}

function CheckRow({ check }: { check: Check }) {
  const right = check.note || (check.duration_ms > 0 ? `${(check.duration_ms / 1000).toFixed(1)}s` : "");
  return (
    <div className="flex items-center gap-2 py-1.5">
      <CIStatus status={check.status as CIStatusValue} />
      <code className="min-w-0 flex-1 truncate font-mono text-[11px]">{check.name}</code>
      <span className="font-mono text-[10px] text-faint">{right}</span>
    </div>
  );
}
