package api

import (
	"path/filepath"
	"strings"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
)

// detectPrimaryLang picks a PR's primary language by majority vote over file extensions, for the /history language filter segment.
// Rules:
// - skip common lockfiles (package-lock.json / go.sum / Cargo.lock, etc.); they are large in line count but semantically not code
// - only extensions in the langByExt table count; anything unlisted (.md / .txt / .yml, etc.) is ignored
// - votes are counted per file, not per changed line, so one big document cannot steal the language label for a whole PR
// - returns "" when nothing recognizable is found
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
		// break ties alphabetically for a fixed result (test stability + consistent user perception)
		if count > bestCount || (count == bestCount && lang < best) {
			best = lang
			bestCount = count
		}
	}
	return best
}

// langByExt maps an extension to the language name shown to users.
// Naming follows the mainstream GitHub Linguist spelling ("Go" not "Golang", "C#" not "CSharp") so the frontend segment can display it directly.
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

// ignoreLockfiles lists the high-frequency non-code filenames skipped when counting votes.
// Matching is by filename rather than extension (.lock is far too broad).
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
