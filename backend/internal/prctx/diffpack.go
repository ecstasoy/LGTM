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
