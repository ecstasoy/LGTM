"use client";

import { useState } from "react";
import { Check, Clipboard, ExternalLink, GitCommit, MessageSquare, Sparkle, X } from "lucide-react";

import type { Suggestion } from "@/lib/types";
import { cn } from "@/lib/utils";
import { signInURL } from "@/lib/auth";
import { ApiError, friendlyError } from "@/lib/errors";
import { CategoryBadge, type Category } from "@/components/ui/badge";
import { useAdopt } from "./AdoptContext";
import { useT } from "@/lib/i18n/context";

interface Props {
  suggestion: Suggestion;
  onCopy?: () => void;
}

type ActionState =
  | { kind: "idle" }
  | { kind: "posting" }
  | { kind: "done"; url: string; commentID?: number; commitSHA?: string; halfDone?: boolean; halfDoneReason?: string }
  | { kind: "undoing" }
  | { kind: "undone" }
  // code: the backend's stable error code (see backend/internal/api/errcode.go), kept alongside the
  // raw message so the render site can resolve localized copy instead of printing the Chinese message.
  | { kind: "error"; msg: string; code?: string };

// InlineSuggestion 行内建议气泡（DiffView 内嵌锚定到对应代码行）
// 严格对齐 design 原型 Diff.jsx 的 InlineSuggestion：surface-2 底 + 左 padding 50px
// 4 按钮：💬 评论 (G6b)、✅ 提交 (G6c stub)、📋 复制 markdown、✕ 忽略
// 评论/提交按钮按 useAdopt() 返的 perms 状态 disable + 悬浮 tooltip 说明原因
export function InlineSuggestion({ suggestion, onCopy }: Props) {
  const t = useT();
  const adopt = useAdopt();
  const [copied, setCopied] = useState(false);
  const [comment, setComment] = useState<ActionState>({ kind: "idle" });
  const [commit, setCommit] = useState<ActionState>({ kind: "idle" });
  const [dismissed, setDismissed] = useState(false);

  if (dismissed) return null;

  function copy() {
    const md = formatAsMarkdown(suggestion);
    navigator.clipboard.writeText(md).then(() => {
      setCopied(true);
      onCopy?.();
      setTimeout(() => setCopied(false), 1400);
    });
  }

  async function postComment() {
    if (!adopt) return;
    setComment({ kind: "posting" });
    try {
      const r = await adopt.postComment(suggestion);
      setComment({ kind: "done", url: r.html_url ?? "", commentID: r.comment_id });
      adopt.markAdopted(suggestion);
    } catch (e) {
      setComment(toErrorState(e));
    }
  }

  async function undoComment() {
    if (!adopt || comment.kind !== "done" || !comment.commentID) return;
    const cid = comment.commentID;
    setComment({ kind: "undoing" });
    try {
      await adopt.deleteComment(cid);
      setComment({ kind: "undone" });
      adopt.markUnadopted(suggestion);
    } catch (e) {
      setComment(toErrorState(e));
    }
  }

  async function postCommit() {
    if (!adopt) return;
    setCommit({ kind: "posting" });
    try {
      const r = await adopt.postCommit(suggestion);
      setCommit({
        kind: "done",
        url: r.html_url ?? "",
        commitSHA: r.commit_sha,
        halfDone: r.comment_posted_but_commit_failed,
        halfDoneReason: r.commit_fail_reason,
      });
      // commit 算采纳；half-done 也算（comment 上 PR 了）
      adopt.markAdopted(suggestion);
    } catch (e) {
      setCommit(toErrorState(e));
    }
  }

  // 按钮可用性判定：reviewId + 登录 + 权限 + PR 状态
  const perms = adopt?.perms ?? null;
  const reviewReady = !!adopt?.reviewId;
  const authenticated = !!perms?.authenticated;

  // PR 状态：backend 已收成 open/closed/merged 三态（real_fetcher.go）
  // merged: head branch 通常已删 → commit 必失败 → 禁
  // closed: PR 已关闭 → commit 没意义 → 禁；comment 可发但弱化
  // open（默认）：按 perms 决定
  const prState = adopt?.prMeta?.state ?? "open";
  const prMerged = prState === "merged";
  const prClosed = prState === "closed";
  const prInactive = prMerged || prClosed;

  const commentEnabled = reviewReady && authenticated && (perms?.can_comment ?? false);
  // commit 在 PR 已 merged / closed 时强制禁，无视 perm
  const commitEnabled = !prInactive && reviewReady && authenticated && (perms?.can_commit ?? false);

  // permsReason resolves /api/perms' reason_code through the dictionary; perms.reason itself is the
  // backend's Chinese prose and is only the fallback for the codeless branch (GitHub perms fetch failed).
  function permsReason(): string {
    if (!perms?.reason) return "";
    const code = perms.reason_code;
    return (code ? t.errors.byCode[code] : undefined) ?? perms.reason;
  }

  // tooltip 文案：按状态优先级返
  function disableReason(): string {
    if (!reviewReady) return t.review.reviewSavingHint;
    if (!authenticated) return t.review.signInToPostHint;
    return permsReason();
  }

  function commitDisableReason(): string {
    if (prMerged) return t.review.prMergedCommitDisabledReason;
    if (prClosed) return t.review.prClosedCommitDisabledReason;
    if (!reviewReady) return disableReason();
    if (!authenticated) return t.review.signInToCommitHint;
    if (!(perms?.can_commit)) return permsReason() || t.review.noPushPermissionReason;
    return "";
  }

  // Adopt failures carry the backend's code; resolve once here so the tooltip and the visible line agree.
  const commentError = comment.kind === "error" ? friendlyError(comment.msg, t, comment.code) : "";
  const commitError = commit.kind === "error" ? friendlyError(commit.msg, t, commit.code) : "";

  return (
    <div className="animate-fade-up border-y border-border bg-surface-2 py-2.5 pl-[50px] pr-3.5">
      {prInactive ? (
        <div className="mb-2 inline-flex items-center gap-1.5 rounded-md border border-border bg-surface px-2 py-1 text-[11px] text-muted">
          {prMerged ? "📌" : "🗃"} {t.review.prInactiveBanner(prMerged)}
        </div>
      ) : null}
      <div className="mb-1.5 flex items-center gap-[7px]">
        <span className="inline-flex items-center gap-1 rounded-[5px] bg-accent-soft px-[7px] py-0.5 font-mono text-xs font-semibold text-accent">
          <Sparkle className="h-3 w-3" fill="currentColor" />
          {t.review.aiSuggestionBadge}
        </span>
        <CategoryBadge category={suggestion.type as Category} />
      </div>
      <div className="mb-1 text-sm font-semibold text-text">{suggestion.title}</div>
      <div className={cn("text-xs leading-[1.6] text-text-2", suggestion.patch ? "mb-2.5" : "mb-1")}>
        {suggestion.body}
      </div>

      {suggestion.patch ? (
        <div className="overflow-hidden rounded-md border border-border bg-surface font-mono text-[12.5px]">
          {suggestion.patch.before.split("\n").map((l, i) => (
            <div key={`b-${i}`} className="whitespace-pre-wrap bg-del-bg px-3 py-px">
              <span className="select-none text-fail">- </span>
              {l}
            </div>
          ))}
          {suggestion.patch.after.split("\n").map((l, i) => (
            <div key={`a-${i}`} className="whitespace-pre-wrap bg-add-bg px-3 py-px">
              <span className="select-none text-ok">+ </span>
              {l}
            </div>
          ))}
        </div>
      ) : null}

      <div className="mt-2.5 flex flex-wrap items-center gap-2">
        {/* 💬 评论：G6b */}
        {commentEnabled ? (
          <button
            type="button"
            onClick={postComment}
            disabled={comment.kind === "posting"}
            className="inline-flex h-7 items-center gap-1 rounded-md bg-accent px-2.5 text-xs font-medium text-accent-fg hover:opacity-90 disabled:opacity-60"
            title={t.review.commentButtonTitle}
          >
            <MessageSquare className="h-3 w-3" />
            {comment.kind === "posting" ? t.review.commentSendingLabel : t.review.commentToPrButton}
          </button>
        ) : !authenticated && reviewReady ? (
          <a
            href={signInURL()}
            className="inline-flex h-7 items-center gap-1 rounded-md bg-accent px-2.5 text-xs font-medium text-accent-fg hover:opacity-90"
            title={t.review.signInAfterGithubTitle}
          >
            <MessageSquare className="h-3 w-3" />
            {t.review.signInToCommentButton}
          </a>
        ) : (
          <button
            type="button"
            disabled
            title={disableReason()}
            className="inline-flex h-7 items-center gap-1 rounded-md bg-accent px-2.5 text-xs font-medium text-accent-fg opacity-50 cursor-not-allowed"
          >
            <MessageSquare className="h-3 w-3" />
            {t.review.commentToPrButton}
          </button>
        )}

        {/* ✅ 提交：G6c 真接通；PR merged/closed 时强制禁 */}
        {commitEnabled ? (
          <button
            type="button"
            onClick={postCommit}
            disabled={commit.kind === "posting"}
            className="inline-flex h-7 items-center gap-1 rounded-md border border-border-strong bg-surface px-2.5 text-xs text-text-2 hover:bg-surface-hover hover:text-text disabled:opacity-60"
            title={t.review.commitButtonTitle}
          >
            <GitCommit className="h-3 w-3" />
            {commit.kind === "posting" ? t.review.commitSendingLabel : t.review.commitToPrButton}
          </button>
        ) : (
          <button
            type="button"
            disabled
            title={commitDisableReason()}
            className="inline-flex h-7 items-center gap-1 rounded-md border border-border-strong bg-surface px-2.5 text-xs text-muted opacity-60 cursor-not-allowed"
          >
            <GitCommit className="h-3 w-3" />
            {t.review.commitToPrButton}
          </button>
        )}

        {/* 📋 复制：永远可用 */}
        <button
          type="button"
          onClick={copy}
          className="inline-flex h-7 items-center gap-1 rounded-md border border-border-strong bg-surface px-2.5 text-xs text-text-2 hover:bg-surface-hover hover:text-text"
        >
          {copied ? <Check className="h-3 w-3 text-ok" /> : <Clipboard className="h-3 w-3" />}
          {copied ? t.review.copiedLabel : t.review.copyMarkdownButton}
        </button>

        {/* ✕ 忽略：本地隐藏 */}
        <button
          type="button"
          onClick={() => setDismissed(true)}
          className="inline-flex h-7 items-center gap-1 rounded-md px-2.5 text-xs text-muted hover:text-text"
        >
          <X className="h-3 w-3" />
          {t.review.dismissButton}
        </button>
      </div>

      {/* 评论后反馈 */}
      {comment.kind === "done" ? (
        <p className="mt-2 inline-flex items-center gap-2 text-[11px] text-ok">
          <Check className="h-3 w-3" />
          {t.review.commentPostedLabel}
          {comment.url ? (
            <a
              href={comment.url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-0.5 underline hover:opacity-80"
            >
              {t.review.viewLinkLabel}
              <ExternalLink className="h-2.5 w-2.5" />
            </a>
          ) : null}
          {comment.commentID ? (
            <button
              type="button"
              onClick={undoComment}
              className="inline-flex items-center gap-0.5 text-muted hover:text-high"
              title={t.review.undoCommentTitle}
            >
              <X className="h-2.5 w-2.5" />
              {t.review.undoLabel}
            </button>
          ) : null}
        </p>
      ) : null}
      {comment.kind === "undoing" ? (
        <p className="mt-2 text-[11px] text-muted">{t.review.undoingLabel}</p>
      ) : null}
      {comment.kind === "undone" ? (
        <p className="mt-2 inline-flex items-center gap-1 text-[11px] text-muted">
          <X className="h-3 w-3" />
          {t.review.undoneLabel}
        </p>
      ) : null}
      {comment.kind === "error" ? (
        <p className="mt-2 text-[11px] text-high" title={commentError}>
          {t.review.commentActionFailedPrefix(commentError)}
        </p>
      ) : null}

      {/* 提交后反馈：3 态 */}
      {commit.kind === "done" && !commit.halfDone ? (
        <p className="mt-2 inline-flex items-center gap-1.5 text-[11px] text-ok">
          <Check className="h-3 w-3" />
          {t.review.commitPostedPrefix}
          {commit.commitSHA ? commit.commitSHA.slice(0, 7) : ""}
          {commit.url ? (
            <a
              href={commit.url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-0.5 underline hover:opacity-80"
            >
              {t.review.viewLinkLabel}
              <ExternalLink className="h-2.5 w-2.5" />
            </a>
          ) : null}
        </p>
      ) : null}
      {commit.kind === "done" && commit.halfDone ? (
        <p className="mt-2 text-[11px] text-med" title={commit.halfDoneReason}>
          {t.review.commitHalfDoneReason}
          {commit.url ? (
            <a
              href={commit.url}
              target="_blank"
              rel="noreferrer"
              className="ml-1 inline-flex items-center gap-0.5 underline hover:opacity-80"
            >
              {t.review.manualApplyLinkText}
              <ExternalLink className="h-2.5 w-2.5" />
            </a>
          ) : null}
        </p>
      ) : null}
      {commit.kind === "error" ? (
        <p className="mt-2 text-[11px] text-high" title={commitError}>
          {t.review.commitActionFailedPrefix(commitError)}
        </p>
      ) : null}
    </div>
  );
}

// toErrorState keeps ApiError's code with the message so the render site can localize it.
function toErrorState(e: unknown): ActionState {
  if (e instanceof ApiError) return { kind: "error", msg: e.message, code: e.code };
  return { kind: "error", msg: e instanceof Error ? e.message : String(e) };
}

// formatAsMarkdown 把建议格式化成可直接照搬进 GitHub PR comment 的 markdown
function formatAsMarkdown(s: Suggestion): string {
  const parts: string[] = [];
  parts.push(`**${s.title}**`);
  parts.push("");
  parts.push(s.body);
  if (s.patch) {
    parts.push("");
    parts.push("```" + (s.patch.lang || ""));
    parts.push("// before");
    parts.push(s.patch.before);
    parts.push("// after");
    parts.push(s.patch.after);
    parts.push("```");
  }
  return parts.join("\n");
}
