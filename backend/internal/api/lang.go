package api

import (
	"path/filepath"
	"strings"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
)

// detectPrimaryLang 按文件名后缀多数派算 PR 的主语言，给 /history 的语言筛选段控用。
// 规则：
//   - 跳过常见 lockfile（package-lock.json / go.sum / Cargo.lock 等），它们行数大但语义"非代码"
//   - 仅在 langByExt 表里查；未列出后缀（.md / .txt / .yml 等）不计入
//   - 计票按"文件数"而非加减行数，避免单 PR 内一个大文档 PR 抢了语言标签
//   - 完全无可识别语言时返 ""
func detectPrimaryLang(files []gh.File) string {
	counts := map[string]int{}
	for _, f := range files {
		base := filepath.Base(f.Path)
		if ignoreLockfiles[base] {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Path))
		if lang, ok := langByExt[ext]; ok {
			counts[lang]++
		}
	}
	var best string
	var bestCount int
	for lang, count := range counts {
		// 同票时按字母序固定（test 稳定性 + 用户感知一致）
		if count > bestCount || (count == bestCount && lang < best) {
			best = lang
			bestCount = count
		}
	}
	return best
}

// langByExt 后缀 → 用户面前显示用的语言名。
// 命名沿用 GitHub Linguist 主流社区写法（"Go" 不 "Golang"，"C#" 不 "CSharp"），方便前端段控直接展示。
var langByExt = map[string]string{
	".go":     "Go",
	".ts":     "TypeScript",
	".tsx":    "TypeScript",
	".js":     "JavaScript",
	".jsx":    "JavaScript",
	".mjs":    "JavaScript",
	".cjs":    "JavaScript",
	".py":     "Python",
	".rs":     "Rust",
	".java":   "Java",
	".kt":     "Kotlin",
	".kts":    "Kotlin",
	".swift":  "Swift",
	".rb":     "Ruby",
	".php":    "PHP",
	".cs":     "C#",
	".cpp":    "C++",
	".cc":     "C++",
	".cxx":    "C++",
	".hpp":    "C++",
	".c":      "C",
	".h":      "C",
	".m":      "Objective-C",
	".mm":     "Objective-C++",
	".scala":  "Scala",
	".sc":     "Scala",
	".dart":   "Dart",
	".lua":    "Lua",
	".r":      "R",
	".ex":     "Elixir",
	".exs":    "Elixir",
	".erl":    "Erlang",
	".hs":     "Haskell",
	".clj":    "Clojure",
	".cljs":   "Clojure",
	".ml":     "OCaml",
	".elm":    "Elm",
	".nim":    "Nim",
	".zig":    "Zig",
	".sh":     "Shell",
	".bash":   "Shell",
	".zsh":    "Shell",
	".fish":   "Shell",
	".ps1":    "PowerShell",
	".pl":     "Perl",
	".pm":     "Perl",
	".groovy": "Groovy",
	".gradle": "Groovy",
	".f":      "Fortran",
	".f90":    "Fortran",
	".sql":    "SQL",
	".html":   "HTML",
	".htm":    "HTML",
	".css":    "CSS",
	".scss":   "SCSS",
	".sass":   "Sass",
	".vue":    "Vue",
	".svelte": "Svelte",
}

// ignoreLockfiles 计票时跳过的"非代码"高频文件名。
// 不跳后缀（.lock 太宽泛），按文件名匹配最准。
var ignoreLockfiles = map[string]bool{
	"package-lock.json": true,
	"pnpm-lock.yaml":    true,
	"yarn.lock":         true,
	"go.sum":            true,
	"Cargo.lock":        true,
	"composer.lock":     true,
	"Gemfile.lock":      true,
	"Pipfile.lock":      true,
	"poetry.lock":       true,
	"bun.lockb":         true,
}
