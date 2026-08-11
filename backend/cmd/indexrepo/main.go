// indexrepo 把整个 repo 离线切 chunks 入 RAG SQLite DB，作"全仓预索引"。
// 跟运行时 indexPRChunks 互补：那个只索引被评过 PR 的 diff；这个让 RAG 库一上来就有全仓内容。
//
// 用法：
//
//	cd backend
//	go run ./cmd/indexrepo \
//	    --scope ecstasoy/LGTM \
//	    --dir .. \
//	    --db ./data/rag.db
//
// embedder 配置走 .env（同 server）：EMBEDDING_PROVIDER / _API_KEY / _BASE_URL / _MODEL
// PRNumber=0 标记离线索引（runtime PR 索引带 PR 号；前端模板 if gt .PRNumber 0 区分显示）
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"github.com/ecstasoy/LGTM/backend/internal/index"
)

const (
	// chunkChars 单 chunk 目标字符；text-embedding-3-small 8192 token 上限 ~ 30K 字符，取保守
	chunkChars = 3000
	// chunkOverlap 相邻 chunk 重叠字符，避免函数 / 类定义被切两半丢上下文
	chunkOverlap = 300
	// maxFileBytes 跳过过大文件（生成代码 / 大 SQL dump / minified js 等）
	maxFileBytes = 200 * 1024
	// batchSize 单次 UpsertMany 的 chunk 数；越大 embedding API 单批越大，但失败成本越高
	batchSize = 32
	// textProbeBytes 探测前 N 字节判定 binary（有 null 字节即跳）
	textProbeBytes = 8192
)

// includeExts 索引这些后缀的文件；白名单避免索引图片 / 二进制 / 锁文件
// 增减时考虑：是否对评审有帮助（代码 / 文档 yes；lock 文件 / minified js no）
var includeExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".rb": true, ".java": true, ".kt": true, ".rs": true, ".swift": true,
	".c": true, ".cc": true, ".cpp": true, ".h": true, ".hpp": true,
	".cs": true, ".php": true, ".scala": true,
	".md": true, ".rst": true, ".txt": true,
	".yaml": true, ".yml": true, ".toml": true, ".json": true, ".sql": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true,
	".css": true, ".scss": true, ".less": true, ".html": true,
	".tmpl": true, ".tpl": true, ".jinja": true, ".mustache": true,
	".dockerfile": true, ".env.example": true,
}

// skipDirs 不进的目录（依赖 / 构建产物 / 大数据 / 工具私有）
var skipDirs = map[string]bool{
	".git": true, ".next": true, ".vercel": true, ".cache": true,
	"node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, "out": true, ".turbo": true,
	"__pycache__": true, ".pytest_cache": true, ".venv": true, "venv": true,
	".idea": true, ".vscode": true,
	".claude": true, // Claude Code 私有目录（notes / settings），通常 gitignored 不应喂给 RAG
	"data":    true, // RAG 自己的 SQLite 也在 data/，避免索引到 .db 文件
}

// skipFiles 个别明确不要的文件名
var skipFiles = map[string]bool{
	"package-lock.json": true,
	"pnpm-lock.yaml":    true,
	"yarn.lock":         true,
	"go.sum":            true,
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	var (
		scope   = flag.String("scope", "", "命名空间，通常 owner/repo（必填）")
		repoDir = flag.String("dir", ".", "本地 repo 根目录")
		dbPath  = flag.String("db", "./data/rag.db", "SQLite RAG DB 路径")
		envPath = flag.String("env", ".env", ".env 文件路径（含 EMBEDDING_*）")
		dryRun  = flag.Bool("dry-run", false, "只扫描列出待索引文件，不真打 embedding API")
	)
	flag.Parse()
	if *scope == "" {
		fmt.Fprintln(os.Stderr, "ERROR: --scope is required (e.g. owner/repo)")
		flag.Usage()
		os.Exit(2)
	}

	if err := godotenv.Load(*envPath); err != nil && !os.IsNotExist(err) {
		slog.Warn("load env file failed; relying on shell env", "path", *envPath, "err", err)
	}

	emb := pickEmbedder()

	// 扫文件 → chunks
	chunks, files, skipped, err := scanRepo(*repoDir)
	if err != nil {
		slog.Error("scan repo failed", "err", err)
		os.Exit(1)
	}
	slog.Info("scan done", "files_indexed", files, "files_skipped", skipped, "chunks", len(chunks))

	if *dryRun {
		fmt.Println("--- dry-run: chunks (path|idx|chars) ---")
		for _, c := range chunks {
			fmt.Printf("%s|%d|%d\n", c.Path, c.Idx, len(c.Content))
		}
		return
	}
	if len(chunks) == 0 {
		slog.Info("nothing to index")
		return
	}

	// 确保 DB 目录存在
	if dir := filepath.Dir(*dbPath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	rt, err := index.NewSQLiteRetriever(*dbPath, emb)
	if err != nil {
		slog.Error("open retriever failed", "err", err)
		os.Exit(1)
	}
	defer rt.Close()

	ctx := context.Background()
	indexed := 0
	for i := 0; i < len(chunks); i += batchSize {
		end := min(i+batchSize, len(chunks))
		batch := chunks[i:end]
		// retry 2 次：容器 boot 时跟 server 共享 rag.db，前几个 batch 可能撞 SQLite
		// 初始化 race（CREATE TABLE / pragma migration）；重试覆盖瞬态错误
		var err error
		for attempt := 0; attempt < 3; attempt++ {
			if err = rt.UpsertMany(ctx, *scope, batch); err == nil {
				break
			}
			if attempt < 2 {
				time.Sleep(time.Duration(100*(attempt+1)) * time.Millisecond)
			}
		}
		if err != nil {
			slog.Warn("batch upsert failed after retries; continuing next batch", "range", fmt.Sprintf("[%d:%d]", i, end), "err", err)
			continue
		}
		indexed += len(batch)
		slog.Info("batch done", "indexed", indexed, "total", len(chunks))
	}
	slog.Info("index complete", "scope", *scope, "chunks_written", indexed)
}

// scanRepo 遍历 repoDir 按白名单后缀收文件 → 切 chunks
// 返回 (chunks, 索引的文件数, 跳过的文件数, err)
func scanRepo(repoDir string) ([]index.IndexerChunk, int, int, error) {
	var chunks []index.IndexerChunk
	var fileCount, skipCount int

	err := filepath.WalkDir(repoDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // 个别文件读不到不阻塞整体
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFiles[d.Name()] {
			skipCount++
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if !includeExts[ext] {
			skipCount++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			skipCount++
			return nil
		}
		if info.Size() > maxFileBytes {
			skipCount++
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			skipCount++
			return nil
		}
		if !isLikelyText(raw) {
			skipCount++
			return nil
		}
		rel, err := filepath.Rel(repoDir, p)
		if err != nil {
			rel = p
		}
		// 路径分隔符归一化（Windows 时统一 / ）
		rel = filepath.ToSlash(rel)
		for idx, piece := range splitToWindows(string(raw)) {
			chunks = append(chunks, index.IndexerChunk{
				Path:     rel,
				Idx:      idx,
				Content:  piece,
				PRNumber: 0, // 0 表示离线索引；前端 prompt 模板会因此不显示「来自 PR」前缀
			})
		}
		fileCount++
		return nil
	})
	return chunks, fileCount, skipCount, err
}

// splitToWindows 滑窗切：每 chunkChars 字符一段，相邻段重叠 chunkOverlap
// 短文件（< chunkChars）直接返回一段
// 切窗末尾若不在行边界，回溯到最近 \n（最多回溯 100 字符）让 chunk 不在词中间断开
func splitToWindows(s string) []string {
	if len(s) <= chunkChars {
		return []string{s}
	}
	var out []string
	step := chunkChars - chunkOverlap
	for i := 0; i < len(s); i += step {
		end := min(i+chunkChars, len(s))
		// 回溯到最近行尾（避免切在词中间）
		if end < len(s) {
			for back := 0; back < 100 && end-back > i; back++ {
				if s[end-back-1] == '\n' {
					end -= back
					break
				}
			}
		}
		out = append(out, s[i:end])
		if end == len(s) {
			break
		}
	}
	return out
}

// isLikelyText 探测前 N 字节有无 null byte；UTF-8 文本不含 \x00
func isLikelyText(b []byte) bool {
	n := min(len(b), textProbeBytes)
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return false
		}
	}
	return true
}

// pickEmbedder 同 cmd/server 的逻辑；缺 key 自动降级 mock 让 dry-run 也能 sanity check
func pickEmbedder() index.Embedder {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("EMBEDDING_PROVIDER")))
	switch provider {
	case "openai":
		key := os.Getenv("EMBEDDING_API_KEY")
		if key == "" {
			slog.Warn("EMBEDDING_PROVIDER=openai 但 EMBEDDING_API_KEY 未设；降级 mock（向量无语义）")
			return index.NewMockEmbedder()
		}
		base := os.Getenv("EMBEDDING_BASE_URL")
		if base == "" {
			base = "https://api.openai.com"
		}
		model := os.Getenv("EMBEDDING_MODEL")
		if model == "" {
			model = "text-embedding-3-small"
		}
		slog.Info("embedder ready", "type", "openai", "base", base, "model", model)
		return index.NewOpenAIEmbedder(base, key, model)
	case "mock", "":
		slog.Info("embedder ready", "type", "mock")
		return index.NewMockEmbedder()
	default:
		slog.Warn("未知 EMBEDDING_PROVIDER；降级 mock", "value", provider)
		return index.NewMockEmbedder()
	}
}
