// Types shared between frontend and backend.
// Field names match the backend's JSON (snake_case) to avoid conversion.

export interface Risk {
  file: string;
  line?: number;
  severity: "high" | "medium" | "low";
  category: "bug" | "security" | "perf" | "style" | "concurrency" | "breaking" | "other";
  confidence: number; // 0-1, the LLM's self-rated confidence; >= 0.9 expands by default
  reason: string;
}

export interface Patch {
  lang: string;
  before: string;
  after: string;
}

// File: a changed file in the PR; the raw unified diff text lives in the patch field.
export interface File {
  path: string;
  status: "added" | "modified" | "removed" | "renamed";
  patch: string;
  additions: number;
  deletions: number;
}

export interface Suggestion {
  file: string;
  line: number;
  type: "bug" | "style" | "perf" | "security" | "concurrency";
  title: string;
  body: string;
  patch?: Patch | null; // null/omitted when the LLM can't produce a concrete code rewrite
}

// Stats: PR size stats.
export interface Stats {
  files: number;
  additions: number;
  deletions: number;
  commits: number;
  comments: number;
}

// Check: a single CI check run.
export interface Check {
  name: string;
  status: "passing" | "failing" | "pending" | string; // tolerate unknown values
  duration_ms: number;
  note?: string; // check-run output.summary (e.g. coverage "82.4% (-0.3%)")
}

// PrMeta: PR metadata shared by the review SSE `pr` event and the detail endpoint.
// Fields align with gh.PullRequest (A1/A2/A3/author_role PR), surfaced via prMetaPayload + cachedPayload.
export interface PrMeta {
  id: string;
  owner: string;
  repo: string;
  pr: number;
  url: string;
  head_sha: string;
  title: string;
  author?: string;
  author_role?: string; // OWNER / MEMBER / COLLABORATOR / CONTRIBUTOR / FIRST_TIMER / NONE
  state?: string; // open / closed / merged
  labels?: string[];
  base_ref?: string;
  head_ref?: string;
  pr_created_at?: string; // RFC3339
  stats?: Stats;
  ci?: "passing" | "failing" | "pending" | string;
  checks?: Check[];
  // source is only returned by the detail endpoint; the frontend uses it to render the top-bar auto-review chip.
  // Not sent during streaming, so it's optional.
  source?: "manual" | "webhook";
}

// BudgetReport: the three-layer context's actual token budget allocation; the backend's
// prctx.LayeredBuilder output, shared by the SSE budget_report frame and detail.budget_report.
export interface BudgetReport {
  token_limit: number;
  used_l1: number;
  used_l2: number;
  used_l3: number;
  used_l4?: number; // only used by v3 RAG
  dropped?: string[]; // file paths dropped entirely for budget reasons
}

// ReviewSummary: an /api/reviews list item; does not include the payload.
export interface ReviewSummary {
  id: string;
  owner: string;
  repo: string;
  pr: number;
  head_sha: string;
  title?: string;
  created_at: string; // RFC3339 (when the review record was created)
  ci?: string;
  lang?: string; // the PR's primary programming language (Go / TypeScript / Python / …); the backend picks the majority by file extension
  source?: "manual" | "webhook"; // webhook-triggered auto review; the list renders an auto chip
  created_by?: string; // GitHub login; empty means an anonymous legacy record; the frontend uses this to gate the delete button
  risk_counts?: { high: number; medium: number; low: number };
}

// ReviewDetail: an /api/reviews/:id detail response; the inline-cached payload plus the full PR meta.
// Note: source is inherited via ReviewSummary; the frontend can read detail.source directly.
export interface ReviewDetail extends ReviewSummary {
  // PR meta (A1+A2+A3+author_role+lang)
  author?: string;
  author_role?: string;
  state?: string;
  labels?: string[];
  base_ref?: string;
  head_ref?: string;
  pr_created_at?: string;
  stats?: Stats;
  checks?: Check[];

  files?: File[];
  summary: string;
  risks?: Risk[];
  suggestions?: Suggestion[];
  budget_report?: BudgetReport;
}

// Legacy export kept for compatibility (lib/api.ts's ReviewResult type has no active consumers).
export interface ReviewResult {
  id: string;
  owner: string;
  repo: string;
  pr: number;
  url: string;
  head_sha: string;
  title: string;
  summary: string;
  risks?: Risk[];
  suggestions?: Suggestion[];
}

// AgentToolCall: a single agent-loop tool call (matches the backend SSE tool_call_start/done frame's data field).
export interface AgentToolCall {
  id: string;
  name: string;
  arguments?: string; // present on the start frame; not repeated on the done frame
  result?: string;    // present on the done frame; may contain an "error: ..." string
}

export type EventType =
  | "summary_delta"
  | "risks_done"
  | "suggestions_done"
  | "steered_risks_done"
  | "steered_suggestions_done"
  | "files"
  | "budget_report"
  | "tool_call_start"
  | "tool_call_done"
  | "agent_text_delta"
  | "info"
  | "error"
  | "review_id"
  | "done";

export interface SseEvent {
  type: EventType;
  data: unknown;
}
