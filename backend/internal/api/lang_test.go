package api

import (
	"testing"

	gh "github.com/ecstasoy/LGTM/backend/internal/github"
)

func TestDetectPrimaryLang(t *testing.T) {
	cases := []struct {
		name  string
		files []gh.File
		want  string
	}{
		{
			name:  "empty",
			files: nil,
			want:  "",
		},
		{
			name: "single Go file",
			files: []gh.File{
				{Path: "main.go"},
			},
			want: "Go",
		},
		{
			name: "Go majority over README",
			files: []gh.File{
				{Path: "internal/cache/shard.go"},
				{Path: "internal/cache/ttl.go"},
				{Path: "README.md"}, // .md 不在 langByExt，不计票
			},
			want: "Go",
		},
		{
			name: "TS over JS by count",
			files: []gh.File{
				{Path: "src/index.ts"},
				{Path: "src/utils.ts"},
				{Path: "src/legacy.js"},
			},
			want: "TypeScript",
		},
		{
			name: "lockfile ignored",
			files: []gh.File{
				{Path: "package-lock.json"},   // 跳过，否则 .json 也未列出（不计）
				{Path: "src/index.ts"},
			},
			want: "TypeScript",
		},
		{
			name: "Cargo.lock ignored",
			files: []gh.File{
				{Path: "Cargo.lock"},
				{Path: "src/main.rs"},
				{Path: "src/lib.rs"},
			},
			want: "Rust",
		},
		{
			name: "uppercase extension matched case-insensitively",
			files: []gh.File{
				{Path: "Main.GO"},
				{Path: "util.Go"},
			},
			want: "Go",
		},
		{
			name: "no recognized langs",
			files: []gh.File{
				{Path: "README.md"},
				{Path: "config.yml"},
				{Path: "data.txt"},
			},
			want: "",
		},
		{
			name: "tie broken by alphabetical order (Go before Python)",
			files: []gh.File{
				{Path: "a.go"},
				{Path: "b.py"},
			},
			want: "Go", // 平票时按字母序固定
		},
		{
			name: "tie Python vs Rust (Python before Rust)",
			files: []gh.File{
				{Path: "a.py"},
				{Path: "b.rs"},
			},
			want: "Python",
		},
		{
			name: "C++ from .cpp",
			files: []gh.File{
				{Path: "src/foo.cpp"},
				{Path: "include/foo.hpp"},
			},
			want: "C++",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectPrimaryLang(tc.files); got != tc.want {
				t.Errorf("got=%q want=%q", got, tc.want)
			}
		})
	}
}
