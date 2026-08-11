package prctx

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
)

const (
	// DefaultTokenLimit 默认 token 上限（DeepSeek 64K 输入 - 16K 输出预留）
	DefaultTokenLimit = 48000

	// charsPerToken 粗略估算：中文 ~2 char/token，英文 ~4 char/token，取保守均值 3
	charsPerToken = 3

	// floorL2Tokens L2 至少保留的预算，避免大 L3 / L1 / L4 把 L2 挤光
	floorL2Tokens = 1000

	// maxL1Files L1 逐行列出的文件上限；分页后大 PR 可能上千文件，
	// 全列会让 L1 自身撑爆预算并 hard-error。超出只列前 N 个 + 省略计数。
	maxL1Files = 300

	// defaultRAGTopK 默认召回数；v3 调参可调
	defaultRAGTopK = 4

	// defaultRAGScoreThreshold cosine 相似度阈值；< 阈值的召回直接丢
	// 经验校准（text-embedding-3-small）：
	//   - 跨中英语义匹配（中文 query vs 英文代码+中文注释）通常落在 0.35-0.50
	//   - 同语言（query 含目标 token）一般 0.50+
	// 初版 0.5 过严：实测 query="Indexer 接口在哪定义？" → retriever.go cosine=0.463 被错杀
	// 调到 0.35 配合 top-K=4 + L2 path 去重，足够过滤无关召回又不误伤跨语言匹配
	defaultRAGScoreThreshold = 0.35
)

// LayeredBuilder 实现 Builder，按 L1:L2:L3 = 4:5:1 分配 token 预算，
// 超限时按 L3 → L2 → L1 顺序压缩。
type LayeredBuilder struct {
	TokenLimit int
	Retriever  index.Retriever // v1 注入 NoopRetriever；v2 RAG 填 L4
}

// Option 构造 LayeredBuilder 的可选参数。
type Option func(*LayeredBuilder)

// WithTokenLimit 覆盖默认 token 上限。
func WithTokenLimit(n int) Option {
	return func(b *LayeredBuilder) { b.TokenLimit = n }
}

// WithRetriever 注入 RAG 检索器（v1 默认 NoopRetriever）。
func WithRetriever(r index.Retriever) Option {
	return func(b *LayeredBuilder) { b.Retriever = r }
}

// NewLayeredBuilder 构造一个带默认值的 LayeredBuilder。
func NewLayeredBuilder(opts ...Option) *LayeredBuilder {
	b := &LayeredBuilder{
		TokenLimit: DefaultTokenLimit,
		Retriever:  index.NoopRetriever{},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Build 按预算分配 + 压缩生成 Context。
//
// 预算分配（含 L4 RAG）：
//   - L1: 永远全留（超 limit 直接报错）
//   - L3: TokenLimit * 10%
//   - L4: TokenLimit * 20%（Retriever 非 Noop 时启用；Noop 时 0）
//   - L2: TokenLimit - L1 - L3 - L4，至少保留 floorL2Tokens
//
// 引入 L4 后整体比例约 3:4:1:2（与 design README §未来扩展 一致）；
// Retriever=Noop 时 L4=0，等价回退到原 4:5:1。
func (b *LayeredBuilder) Build(ctx context.Context, pr github.PullRequest) (Context, error) {
	return b.BuildWith(ctx, pr, BuildOptions{})
}

// BuildWith 同 Build，但接受 opts；目前 opts.RAGQuery 覆盖 buildL4 的查询 string
// 追问场景应该传 user 输入而非 L1Meta；让召回更对题
func (b *LayeredBuilder) BuildWith(ctx context.Context, pr github.PullRequest, opts BuildOptions) (Context, error) {
	if b.TokenLimit <= 0 {
		return Context{}, fmt.Errorf("token limit must be positive: %d", b.TokenLimit)
	}

	// L1：PR meta + per-file 加减行统计，体量小，永远全留
	l1Str := buildL1Meta(pr)
	l1Tokens := estimateTokens(l1Str)
	if l1Tokens > b.TokenLimit {
		return Context{}, fmt.Errorf("L1 meta exceeds token limit: used=%d limit=%d", l1Tokens, b.TokenLimit)
	}

	// L3：约定文件（v1 Fetcher 暂不填 Conventions，这里安全处理空值）
	l3StrFull := buildL3Conventions(pr.Conventions)
	l3Budget := b.TokenLimit / 10 // 10% 预算
	maxL3Budget := b.TokenLimit - l1Tokens - floorL2Tokens
	if maxL3Budget < 0 {
		maxL3Budget = 0
	}
	if l3Budget > maxL3Budget {
		l3Budget = maxL3Budget
	}

	l3Str := l3StrFull
	if l3Budget == 0 {
		l3Str = ""
	} else if estimateTokens(l3Str) > l3Budget {
		l3Str = truncate(l3Str, l3Budget*charsPerToken)
	}
	l3Tokens := estimateTokens(l3Str)

	// L4：跨文件 RAG 检索；Retriever 非 Noop 时启用
	// 默认 query 用 L1Meta（PR 元信息），opts.RAGQuery 非空时优先用（追问场景传 user 问题）
	// scope = owner/repo 避免跨仓库串扰
	ragQuery := opts.RAGQuery
	if ragQuery == "" {
		ragQuery = l1Str
	}
	// L2/L4 path 去重：本 PR 的 file paths 喂给 L4 让它跳过；避免 L4 重复 L2 已有内容
	prFilePaths := make(map[string]bool, len(pr.Files))
	for _, f := range pr.Files {
		prFilePaths[f.Path] = true
	}
	l4Refs, l4Tokens := b.buildL4(ctx, pr, ragQuery, l1Tokens, l3Tokens, prFilePaths)

	// L2 可用预算 = 总 - L1 - L3 - L4（严格不超出 TokenLimit）
	l2Avail := b.TokenLimit - l1Tokens - l3Tokens - l4Tokens
	if l2Avail < 0 {
		l2Avail = 0
	}

	// L2 按 patch 大小逐个塞入；超出预算的文件入 Dropped 列表
	l2Files, l2Used, dropped := allocateL2(pr.Files, l2Avail)
	return Context{
		L1Meta:        l1Str,
		L2Files:       l2Files,
		L3Conventions: l3Str,
		L4References:  l4Refs,
		BudgetReport: BudgetReport{
			TokenLimit: b.TokenLimit,
			UsedL1:     l1Tokens,
			UsedL2:     l2Used,
			UsedL3:     l3Tokens,
			UsedL4:     l4Tokens,
			Dropped:    dropped,
		},
	}, nil
}

// buildL4 调 Retriever 召回，按 L4 预算截断 References 数量 + 单条 snippet 字符。
// 失败时返回 (nil, 0) + warn —— RAG 不可用时整个评审仍要能跑。
// queryStr 默认 L1Meta；追问场景传 user 问题
// skipPaths 本 PR 已在 L2 出现的 path 集合，召回时跳过避免与 L2 重复
func (b *LayeredBuilder) buildL4(
	ctx context.Context,
	pr github.PullRequest,
	queryStr string,
	l1Tokens, l3Tokens int,
	skipPaths map[string]bool,
) ([]index.Reference, int) {
	if b.Retriever == nil {
		return nil, 0
	}
	// NoopRetriever 直接跳过 retrieve 调用；省一次 embed
	if _, isNoop := b.Retriever.(index.NoopRetriever); isNoop {
		return nil, 0
	}

	l4Budget := b.TokenLimit / 5 // 20% 预算
	// 与 L3 同样的 floor 保护：L1+L3+L4+floorL2 ≤ TokenLimit
	maxL4Budget := b.TokenLimit - l1Tokens - l3Tokens - floorL2Tokens
	if maxL4Budget < 0 {
		maxL4Budget = 0
	}
	if l4Budget > maxL4Budget {
		l4Budget = maxL4Budget
	}
	if l4Budget == 0 {
		return nil, 0
	}

	scope := pr.Owner + "/" + pr.Repo
	// query 截前 1K 字符（无论是 L1Meta 还是 user 问题）；embedding API 对超长 input 也有 token 上限
	query := queryStr
	if len(query) > 1024 {
		query = query[:1024]
	}
	// 多召一些备用：阈值过滤 + L2 去重后可能剩不到 K 条
	refs, err := b.Retriever.Retrieve(ctx, scope, query, defaultRAGTopK*3)
	if err != nil {
		slog.Warn("prctx: RAG retrieve failed; skipping L4", "scope", scope, "err", err)
		return nil, 0
	}
	if len(refs) == 0 {
		return nil, 0
	}

	// 过滤：cosine < 阈值（噪音）+ path 在 L2 出现（重复）
	filtered := make([]index.Reference, 0, len(refs))
	for _, r := range refs {
		if r.Score < defaultRAGScoreThreshold {
			continue
		}
		if skipPaths[r.File] {
			continue
		}
		filtered = append(filtered, r)
		if len(filtered) >= defaultRAGTopK {
			break
		}
	}
	if len(filtered) == 0 {
		return nil, 0
	}

	// 按预算截断：单条 snippet 字符不超过 budget/N（均分）
	maxCharsPerRef := (l4Budget * charsPerToken) / len(filtered)
	out := make([]index.Reference, 0, len(filtered))
	used := 0
	for _, r := range filtered {
		snippet := r.Snippet
		if maxCharsPerRef > 0 && len(snippet) > maxCharsPerRef {
			snippet = snippet[:maxCharsPerRef] + "\n...[truncated]"
		}
		tok := estimateTokens(snippet) + estimateTokens(r.File) + estimateTokens(r.Reason)
		if used+tok > l4Budget {
			break
		}
		// 保留所有元数据字段（含 PRNumber + Score）；之前只复制 3 个字段是 P1 bug
		out = append(out, index.Reference{
			File:     r.File,
			Snippet:  snippet,
			Reason:   r.Reason,
			PRNumber: r.PRNumber,
			Score:    r.Score,
		})
		used += tok
	}
	return out, used
}

func buildL1Meta(pr github.PullRequest) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "仓库: %s/%s#%d\n", pr.Owner, pr.Repo, pr.Number)
	fmt.Fprintf(&sb, "标题: %s\n", pr.Title)
	if pr.Body != "" {
		fmt.Fprintf(&sb, "正文:\n%s\n", pr.Body)
	}
	fmt.Fprintf(&sb, "改动 %d 个文件：\n", len(pr.Files))
	for i, f := range pr.Files {
		if i >= maxL1Files {
			fmt.Fprintf(&sb, "- ……以及其余 %d 个文件（未逐一列出）\n", len(pr.Files)-maxL1Files)
			break
		}
		fmt.Fprintf(&sb, "- %s (%s) +%d -%d\n", f.Path, f.Status, f.Additions, f.Deletions)
	}
	return sb.String()
}

func buildL3Conventions(c github.Conventions) string {
	var parts []string
	if c.Readme != "" {
		parts = append(parts, "## README\n"+c.Readme)
	}
	if c.Contributing != "" {
		parts = append(parts, "## CONTRIBUTING\n"+c.Contributing)
	}
	if c.AgentDocs != "" {
		parts = append(parts, "## AGENT DOCS\n"+c.AgentDocs)
	}
	return strings.Join(parts, "\n\n")
}

func allocateL2(files []github.File, budget int) ([]FileContext, int, []string) {
	out := make([]FileContext, 0, len(files))
	dropped := []string{}
	used := 0
	for _, f := range files {
		patchTokens := estimateTokens(f.Patch)
		if used+patchTokens > budget {
			dropped = append(dropped, f.Path)
			continue
		}
		out = append(out, FileContext{
			Path:  f.Path,
			Patch: f.Patch,
		})
		used += patchTokens
	}
	return out, used, dropped
}

// estimateTokens 粗略估 token 数（不引 tiktoken 等真实分词器，按字符数 / 3 近似）。
func estimateTokens(s string) int {
	chars := 0
	for range s {
		chars++
	}
	return (chars + charsPerToken - 1) / charsPerToken
}

// truncate 截断字符串到指定字符数，末尾加省略标记。
func truncate(s string, maxChars int) string {
	if maxChars <= 0 {
		return "\n...[truncated]"
	}
	chars := 0
	for i := range s {
		if chars == maxChars {
			return s[:i] + "\n...[truncated]"
		}
		chars++
	}
	return s
}
