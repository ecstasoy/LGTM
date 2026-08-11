package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
	"github.com/ecstasoy/LGTM/backend/internal/index"
)

// 四个 builtin tool：read_file / list_dir / grep_patches / search_repo
//
// 沙盒设计分两类：
//  1. read_file / list_dir / grep_patches：限本 PR 改动集（in-memory cachedFiles）
//     防 LLM 被 prompt injection 引导任意读 fs。
//  2. search_repo：限 scope（owner/repo）内的预索引 chunk
//     全仓视野走 RAG 召回，不开放裸 fs / 任意 GitHub API。

// NewReadFileTool 读单个改动文件的 patch hunk + 受限元数据。
// args: { "file": "path/to/file" }
// 返回：patch + status + +/- 行数；超出沙盒返 error。
func NewReadFileTool(files []gh.File) Tool {
	byPath := indexByPath(files)
	return &simpleTool{
		spec: ToolSpec{
			Name:        "read_file",
			Description: "读取 PR 内某个改动文件的 diff hunk 和 add/delete 行数。仅限本次 PR 改动集",
			Parameters:  json.RawMessage(`{"type":"object","required":["file"],"properties":{"file":{"type":"string","description":"PR 内某改动文件的相对路径"}}}`),
		},
		run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				File string `json:"file"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", fmt.Errorf("read_file: 参数解析失败: %w", err)
			}
			if a.File == "" {
				return "", errors.New("read_file: file 不能为空")
			}
			f, ok := byPath[a.File]
			if !ok {
				return "", fmt.Errorf("read_file: %q 不在本 PR 改动集（沙盒拒绝）", a.File)
			}
			return fmt.Sprintf("file: %s\nstatus: %s\n+%d -%d\n\npatch:\n%s",
				f.Path, f.Status, f.Additions, f.Deletions, f.Patch), nil
		},
	}
}

// NewListDirTool 列出某目录下的改动文件；prefix 空 = 仓库根
// args: { "prefix": "src/" }
// 返回：每行一个 file path + status；同样限沙盒内
func NewListDirTool(files []gh.File) Tool {
	return &simpleTool{
		spec: ToolSpec{
			Name:        "list_dir",
			Description: "列出 PR 中某目录下的改动文件。prefix 为空时列所有",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"prefix":{"type":"string","description":"目录前缀，如 \"src/\"；为空时列所有改动"}}}`),
		},
		run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Prefix string `json:"prefix"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", fmt.Errorf("list_dir: 参数解析失败: %w", err)
			}
			prefix := strings.TrimSpace(a.Prefix)
			matched := make([]gh.File, 0, len(files))
			for _, f := range files {
				if prefix == "" || strings.HasPrefix(path.Clean(f.Path), prefix) {
					matched = append(matched, f)
				}
			}
			sort.Slice(matched, func(i, j int) bool { return matched[i].Path < matched[j].Path })
			if len(matched) == 0 {
				return fmt.Sprintf("（沙盒内 prefix=%q 无改动文件）", prefix), nil
			}
			var sb strings.Builder
			for _, f := range matched {
				fmt.Fprintf(&sb, "%-9s +%-4d -%-4d  %s\n", f.Status, f.Additions, f.Deletions, f.Path)
			}
			return sb.String(), nil
		},
	}
}

// NewGrepTool 在所有 cached patches 上 grep 字符串或正则。
// args: { "pattern": "TODO", "regex": false (默认), "max_matches": 20 (默认) }
// 返回：每条命中显示 file:line: 上下文行
func NewGrepTool(files []gh.File) Tool {
	return &simpleTool{
		spec: ToolSpec{
			Name:        "grep_patches",
			Description: "在 PR diff 内 grep 字符串或正则；返回 file:patch行号: 命中行（行号为 patch 内序号，非原文件行号）",
			Parameters:  json.RawMessage(`{"type":"object","required":["pattern"],"properties":{"pattern":{"type":"string"},"regex":{"type":"boolean","description":"true 时按正则编译，否则字面匹配"},"max_matches":{"type":"integer","description":"最多返回多少条命中，默认 20"}}}`),
		},
		run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Pattern    string `json:"pattern"`
				Regex      bool   `json:"regex"`
				MaxMatches int    `json:"max_matches"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", fmt.Errorf("grep_patches: 参数解析失败: %w", err)
			}
			if a.Pattern == "" {
				return "", errors.New("grep_patches: pattern 不能为空")
			}
			if a.MaxMatches <= 0 {
				a.MaxMatches = 20
			}
			var matcher func(string) bool
			if a.Regex {
				re, err := regexp.Compile(a.Pattern)
				if err != nil {
					return "", fmt.Errorf("grep_patches: 非法正则: %w", err)
				}
				matcher = re.MatchString
			} else {
				p := a.Pattern
				matcher = func(s string) bool { return strings.Contains(s, p) }
			}
			var hits []string
			truncated := false
			for _, f := range files {
				if f.Patch == "" {
					continue
				}
				for i, line := range strings.Split(f.Patch, "\n") {
					if matcher(line) {
						hits = append(hits, fmt.Sprintf("%s:%d: %s", f.Path, i+1, line))
						if len(hits) >= a.MaxMatches {
							truncated = true
							goto done
						}
					}
				}
			}
		done:
			if len(hits) == 0 {
				return fmt.Sprintf("（pattern=%q 在 %d 个 patch 中无命中）", a.Pattern, len(files)), nil
			}
			out := strings.Join(hits, "\n")
			if truncated {
				out += fmt.Sprintf("\n（已截断到 max_matches=%d）", a.MaxMatches)
			}
			return out, nil
		},
	}
}

// RegisterDefaults 把三个 PR 沙盒工具都注册到 r（不含 search_repo）。
// 调用方在每次 review steer / agent 循环开始时构造 Registry，
// 用本 PR 的 files 绑定沙盒边界。
// 想接 RAG 全仓检索用 RegisterDefaultsWithRAG。
func RegisterDefaults(r *Registry, files []gh.File) {
	r.Register(NewReadFileTool(files))
	r.Register(NewListDirTool(files))
	r.Register(NewGrepTool(files))
}

// RegisterDefaultsWithRAG 在 RegisterDefaults 基础上额外注册 search_repo。
// retriever 为 nil / NoopRetriever / scope 为空 任一条件成立时跳过 search_repo，
// agent 仍可用其余三个 PR 沙盒工具。
func RegisterDefaultsWithRAG(r *Registry, files []gh.File, retriever index.Retriever, scope string) {
	RegisterDefaults(r, files)
	if retriever == nil {
		return
	}
	if _, isNoop := retriever.(index.NoopRetriever); isNoop {
		return
	}
	if strings.TrimSpace(scope) == "" {
		return
	}
	r.Register(NewSearchRepoTool(retriever, scope))
}

// NewSearchRepoTool 让 agent 在 RAG 全仓索引里语义检索代码片段。
// 与 prctx L4 一次性注入不同：agent 可按对话进展不断换 query 精化召回
// （例：第一轮 "config loading" 没命中，第二轮换 "env parse" 命中）。
//
// scope 在构造时 bound，避免 LLM 试图跨仓串扰；retriever 同样在构造时 bound。
// 返回每条 chunk 的 file / score / 可选 PR 号 + 截断后的内容。
func NewSearchRepoTool(retriever index.Retriever, scope string) Tool {
	return &simpleTool{
		spec: ToolSpec{
			Name:        "search_repo",
			Description: "在全仓 RAG 索引中按 query 语义搜索代码片段。用于查找本 PR 改动以外的相关代码（如调用点、类似实现、main 分支既有逻辑）",
			Parameters:  json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string","description":"自然语言查询；建议带具体关键词或函数名"},"top_k":{"type":"integer","description":"返回多少条结果，默认 5，最大 10"}}}`),
		},
		run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Query string `json:"query"`
				TopK  int    `json:"top_k"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return "", fmt.Errorf("search_repo: 参数解析失败: %w", err)
			}
			if strings.TrimSpace(a.Query) == "" {
				return "", errors.New("search_repo: query 不能为空")
			}
			if retriever == nil {
				return "", errors.New("search_repo: retriever 未配置")
			}
			if a.TopK <= 0 {
				a.TopK = 5
			}
			if a.TopK > 10 {
				a.TopK = 10
			}
			refs, err := retriever.Retrieve(ctx, scope, a.Query, a.TopK)
			if err != nil {
				return "", fmt.Errorf("search_repo: 检索失败: %w", err)
			}
			if len(refs) == 0 {
				return fmt.Sprintf("（query=%q 在 scope=%q 无命中；可能 RAG 未索引或全部被阈值过滤）", a.Query, scope), nil
			}
			var sb strings.Builder
			for i, r := range refs {
				snippet := r.Snippet
				const snippetCap = 800
				if len(snippet) > snippetCap {
					snippet = snippet[:snippetCap] + "...（已截断）"
				}
				prTag := ""
				if r.PRNumber > 0 {
					prTag = fmt.Sprintf("  pr=#%d", r.PRNumber)
				}
				fmt.Fprintf(&sb, "[%d] %s  score=%.2f%s\n%s\n", i+1, r.File, r.Score, prTag, snippet)
				if i < len(refs)-1 {
					sb.WriteString("---\n")
				}
			}
			return sb.String(), nil
		},
	}
}

// simpleTool ToolSpec + run 函数的轻量包装；
// 避免每个 builtin tool 都要写一个完整 struct + 方法集
type simpleTool struct {
	spec ToolSpec
	run  func(ctx context.Context, args json.RawMessage) (string, error)
}

func (s *simpleTool) Spec() ToolSpec { return s.spec }
func (s *simpleTool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	return s.run(ctx, args)
}

func indexByPath(files []gh.File) map[string]gh.File {
	m := make(map[string]gh.File, len(files))
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}
