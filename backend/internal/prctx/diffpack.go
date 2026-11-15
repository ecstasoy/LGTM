package prctx

import (
	"path"
	"regexp"
	"strings"

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
