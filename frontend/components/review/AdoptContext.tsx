"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";

import type { PrMeta, Suggestion } from "@/lib/types";
import type { PermsResponse } from "@/lib/perms";
import { useT } from "@/lib/i18n/context";

// AdoptResult 后端 /comment 或 /commit 端点成功返回字段
// CommentPostedButCommitFailed 仅 /commit 端点返：comment 已上 PR 但 GraphQL apply 失败
// （常见 fork PR maintainer_can_modify=false）→ 前端提示用户去 GitHub 手动 Apply
export interface AdoptResult {
  ok: boolean;
  comment_id?: number;
  commit_sha?: string;
  html_url?: string;
  comment_posted_but_commit_failed?: boolean;
  commit_fail_reason?: string;
}

// AdoptContextValue 给 InlineSuggestion 用的 props 集合
// reviewId 缺失（streaming 模式且尚未拿到 review_id 帧）→ 所有按钮 disable
// prMeta 用于 PR 状态感知：merged/closed 时 commit 按钮强制禁，评论按钮加 banner
interface AdoptContextValue {
  reviewId?: string;
  prMeta?: PrMeta;
  perms: PermsResponse | null;
  permsLoading: boolean;
  // suggestions 完整列表；InlineSuggestion 用 findIndex 找自己 idx
  // 必须用对象身份比较，所以 caller 必须传同一个引用（不要每次 map 出新数组）
  suggestions: Suggestion[];
  // postComment 找到 idx 后调 /api/review/:id/comment/:idx
  postComment: (s: Suggestion) => Promise<AdoptResult>;
  // postCommit 占位；G6c PR 实装
  postCommit: (s: Suggestion) => Promise<AdoptResult>;
  // deleteComment 撤回 PR review comment；cid 是 GitHub databaseId
  deleteComment: (cid: number) => Promise<void>;
  // 已采纳的 suggestion idx 集合；localStorage 按 reviewId 持久；InlineSuggestion 调 markAdopted/markUnadopted
  adoptedIdxs: Set<number>;
  markAdopted: (s: Suggestion) => void;
  markUnadopted: (s: Suggestion) => void;
}

const AdoptContext = createContext<AdoptContextValue | null>(null);

interface ProviderProps {
  reviewId?: string;
  prMeta?: PrMeta;
  perms: PermsResponse | null;
  permsLoading: boolean;
  suggestions: Suggestion[];
  children: ReactNode;
}

// adoptedStorageKey 本地持久化 key 模式；按 reviewId 隔离
function adoptedStorageKey(reviewId: string): string {
  return `lgtm-adopted-${reviewId}`;
}

export function AdoptProvider({ reviewId, prMeta, perms, permsLoading, suggestions, children }: ProviderProps) {
  const t = useT();
  // 已采纳 idx 集合；初始空，hydrate effect 从 localStorage 加载
  const [adoptedIdxs, setAdoptedIdxs] = useState<Set<number>>(() => new Set());

  // hydrate / 切 reviewId 时重读
  useEffect(() => {
    if (!reviewId || typeof window === "undefined") {
      setAdoptedIdxs(new Set());
      return;
    }
    try {
      const raw = window.localStorage.getItem(adoptedStorageKey(reviewId));
      if (raw) {
        const arr = JSON.parse(raw) as number[];
        setAdoptedIdxs(new Set(arr));
      } else {
        setAdoptedIdxs(new Set());
      }
    } catch {
      setAdoptedIdxs(new Set());
    }
  }, [reviewId]);

  const persist = useCallback(
    (s: Set<number>) => {
      if (!reviewId || typeof window === "undefined") return;
      try {
        window.localStorage.setItem(adoptedStorageKey(reviewId), JSON.stringify([...s]));
      } catch {
        // 私密模式 / 满 quota → 静默；UI 仍内存追踪
      }
    },
    [reviewId],
  );

  const markAdopted = useCallback(
    (s: Suggestion) => {
      const idx = suggestions.indexOf(s);
      if (idx < 0) return;
      setAdoptedIdxs((prev) => {
        if (prev.has(idx)) return prev;
        const next = new Set(prev);
        next.add(idx);
        persist(next);
        return next;
      });
    },
    [suggestions, persist],
  );

  const markUnadopted = useCallback(
    (s: Suggestion) => {
      const idx = suggestions.indexOf(s);
      if (idx < 0) return;
      setAdoptedIdxs((prev) => {
        if (!prev.has(idx)) return prev;
        const next = new Set(prev);
        next.delete(idx);
        persist(next);
        return next;
      });
    },
    [suggestions, persist],
  );

  const postComment = useCallback(
    async (s: Suggestion): Promise<AdoptResult> => {
      if (!reviewId) throw new Error(t.review.reviewStreamingError);
      const idx = suggestions.indexOf(s);
      if (idx < 0) throw new Error(t.review.suggestionNotFoundError);
      const res = await fetch(`/api/review/${encodeURIComponent(reviewId)}/comment/${idx}`, {
        method: "POST",
        credentials: "include",
      });
      const data = (await res.json()) as { error?: string } & AdoptResult;
      if (!res.ok || !data.ok) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      return data;
    },
    [reviewId, suggestions, t],
  );

  const postCommit = useCallback(
    async (s: Suggestion): Promise<AdoptResult> => {
      if (!reviewId) throw new Error(t.review.reviewStreamingError);
      const idx = suggestions.indexOf(s);
      if (idx < 0) throw new Error(t.review.suggestionNotFoundError);
      const res = await fetch(`/api/review/${encodeURIComponent(reviewId)}/commit/${idx}`, {
        method: "POST",
        credentials: "include",
      });
      const data = (await res.json()) as { error?: string } & AdoptResult;
      if (!res.ok) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      // 200 + ok=false：comment 上了但 apply 失败（不抛错，让 caller 区分两态显示）
      return data;
    },
    [reviewId, suggestions, t],
  );

  const deleteComment = useCallback(
    async (cid: number): Promise<void> => {
      if (!reviewId) throw new Error(t.review.missingReviewIdError);
      const res = await fetch(`/api/review/${encodeURIComponent(reviewId)}/comment/${cid}`, {
        method: "DELETE",
        credentials: "include",
      });
      const data = (await res.json().catch(() => ({}))) as { error?: string };
      if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
    },
    [reviewId, t],
  );

  const value = useMemo(
    () => ({
      reviewId, prMeta, perms, permsLoading, suggestions,
      postComment, postCommit, deleteComment,
      adoptedIdxs, markAdopted, markUnadopted,
    }),
    [reviewId, prMeta, perms, permsLoading, suggestions, postComment, postCommit, deleteComment, adoptedIdxs, markAdopted, markUnadopted],
  );

  return <AdoptContext.Provider value={value}>{children}</AdoptContext.Provider>;
}

// useAdopt 子组件用；Provider 外调返 null
export function useAdopt(): AdoptContextValue | null {
  return useContext(AdoptContext);
}
