import type { ReviewDetail, ReviewSummary } from "./types";

// postReview 已废弃 —— 后端 /api/review 改为 SSE，请改用 lib/sse.ts 的 streamReview

// ModelOption /api/models 返回项：可选模型白名单（L3）
export interface ModelOption {
  key: string;
  label: string;
}

// StageKey 评审阶段（与后端 stage 名一致）；L3 分阶段选模型用
export type StageKey = "summary" | "risks" | "suggestions";

// STAGES 阶段顺序 + 中文标签（分阶段选择器渲染用）
export const STAGES: { key: StageKey; label: string }[] = [
  { key: "summary", label: "摘要" },
  { key: "risks", label: "风险" },
  { key: "suggestions", label: "建议" },
];

// getModels 拉可选模型列表；失败 / 未配置注册表时返回空数组（前端据此隐藏选择器）
export async function getModels(): Promise<ModelOption[]> {
  try {
    const res = await fetch("/api/models");
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? (data as ModelOption[]) : [];
  } catch {
    return [];
  }
}

// listReviews 拉历史评审列表
export async function listReviews(limit?: number): Promise<ReviewSummary[]> {
  const q = typeof limit === "number" ? `?limit=${limit}` : "";
  const res = await fetch(`/api/reviews${q}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return res.json();
}

// getReview 按 id 拉详情
export async function getReview(id: string): Promise<ReviewDetail> {
  const res = await fetch(`/api/reviews/${encodeURIComponent(id)}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error((body as { error?: string }).error ?? `HTTP ${res.status}`);
  }
  return res.json();
}
