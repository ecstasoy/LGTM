package prctx

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ecstasoy/LGTM/backend/internal/github"
)

// Lockfiles and generated artifacts burn L2 budget without anything a reviewer could act on.
var excludedNames = map[string]bool{
	"go.sum": true, "package-lock.json": true, "npm-shrinkwrap.json": true, "pnpm-lock.yaml": true,
	"yarn.lock": true, "bun.lockb": true, "Cargo.lock": true, "poetry.lock": true, "Pipfile.lock": true,
	"uv.lock": true, "Gemfile.lock": true, "composer.lock": true,
}

var excludedSuffixes = []string{
	".min.js", ".min.css", ".js.map", ".css.map", ".snap",
	".pb.go", ".pb.gw.go", "_pb2.py", "_pb2_grpc.py",
}

var excludedDirs = []string{"vendor/", "node_modules/", "__snapshots__/"}

// generatedHeader matches the Go convention (https://go.dev/s/generatedcode) on an added line.
var generatedHeader = regexp.MustCompile(`(?m)^\+// Code generated .* DO NOT EDIT\.$`)

func isExcluded(f github.File) bool {
	base := path.Base(f.Path)
	if excludedNames[base] {
		return true
	}
	for _, s := range excludedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	for _, d := range excludedDirs {
		if strings.HasPrefix(f.Path, d) || strings.Contains(f.Path, "/"+d) {
			return true
		}
	}
	return generatedHeader.MatchString(f.Patch)
}

// L2 budget goes to source first; docs are the first to lose it.
const (
	rankSource = iota
	rankTest
	rankConfig
	rankDocs
)

var configNames = map[string]bool{"Dockerfile": true, "Makefile": true, ".dockerignore": true, ".gitignore": true}

var configExts = map[string]bool{
	".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".xml": true,
}

var docExts = map[string]bool{".md": true, ".mdx": true, ".rst": true, ".txt": true, ".adoc": true}

func fileRank(p string) int {
	base := path.Base(p)
	ext := path.Ext(base)
	switch {
	case docExts[ext] || strings.HasPrefix(p, "docs/"):
		return rankDocs
	case strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasPrefix(base, "test_") || hasDir(p, "test") || hasDir(p, "tests") || hasDir(p, "__tests__") || hasDir(p, "testdata"):
		return rankTest
	case configNames[base] || configExts[ext] || strings.HasPrefix(p, ".github/"):
		return rankConfig
	}
	return rankSource
}

func hasDir(p, dir string) bool {
	return strings.HasPrefix(p, dir+"/") || strings.Contains(p, "/"+dir+"/")
}

// splitHunks returns the text before the first hunk header and each hunk with its header.
func splitHunks(patch string) (string, []string) {
	var pre, cur []string
	var hunks []string
	in := false
	for _, l := range strings.Split(patch, "\n") {
		if strings.HasPrefix(l, "@@") {
			if in {
				hunks = append(hunks, strings.Join(cur, "\n"))
			}
			cur, in = []string{l}, true
			continue
		}
		if in {
			cur = append(cur, l)
		} else {
			pre = append(pre, l)
		}
	}
	if in {
		hunks = append(hunks, strings.Join(cur, "\n"))
	}
	return strings.Join(pre, "\n"), hunks
}

func deletionOnly(hunk string) bool {
	hasDel := false
	for _, l := range strings.Split(hunk, "\n")[1:] {
		switch {
		case strings.HasPrefix(l, "+"):
			return false
		case strings.HasPrefix(l, "-"):
			hasDel = true
		}
	}
	return hasDel
}

// compressPatch drops hunks that only delete lines; there is nothing new in them to review or anchor to.
func compressPatch(patch string) string {
	pre, hunks := splitHunks(patch)
	var kept []string
	for _, h := range hunks {
		if !deletionOnly(h) {
			kept = append(kept, h)
		}
	}
	omitted := len(hunks) - len(kept)
	if omitted == 0 {
		return patch
	}
	var parts []string
	if pre != "" {
		parts = append(parts, pre)
	}
	parts = append(parts, kept...)
	noun := "hunks"
	if omitted == 1 {
		noun = "hunk"
	}
	parts = append(parts, fmt.Sprintf("... (%d deletion-only %s omitted)", omitted, noun))
	return strings.Join(parts, "\n")
}

// clipMarkerTokens reserves room for the trailing "... [diff clipped ...]" line.
const clipMarkerTokens = 40

// clipPatch keeps whole hunks that fit, or cuts the first hunk at a line boundary; false means not even a line fits.
func clipPatch(patch string, budget int) (string, bool) {
	pre, hunks := splitHunks(patch)
	if len(hunks) == 0 {
		pre, hunks = "", []string{patch}
	}
	// Count runes, not per-line tokens: rounding each line up would waste most of the leftover.
	avail := (budget - clipMarkerTokens - estimateTokens(pre)) * charsPerToken
	var kept []string
	used := 0
	for _, h := range hunks {
		c := utf8.RuneCountInString(h) + 1
		if used+c > avail {
			break
		}
		kept, used = append(kept, h), used+c
	}
	marker := fmt.Sprintf("... [diff clipped to fit the context budget: %d of %d hunks shown]", len(kept), len(hunks))
	if len(kept) == 0 {
		lines := strings.Split(hunks[0], "\n")
		n := 0
		for n < len(lines) && used+utf8.RuneCountInString(lines[n])+1 <= avail {
			used += utf8.RuneCountInString(lines[n]) + 1
			n++
		}
		if n < 2 {
			return "", false
		}
		kept = []string{strings.Join(lines[:n], "\n")}
		marker = fmt.Sprintf("... [diff clipped to fit the context budget: first hunk cut short, 1 of %d hunks shown]", len(hunks))
	}
	parts := kept
	if pre != "" {
		parts = append([]string{pre}, kept...)
	}
	return strings.Join(append(parts, marker), "\n"), true
}
