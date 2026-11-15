package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// makeFiles 造 n 条假 PR 文件，供分页测试用。
func makeFiles(prefix string, n int) []map[string]any {
	out := make([]map[string]any, 0, n)
	for i := range n {
		out = append(out, map[string]any{
			"filename":  fmt.Sprintf("%s%d.go", prefix, i),
			"status":    "modified",
			"patch":     "@@ -1 +1 @@\n-a\n+b",
			"additions": 1,
			"deletions": 0,
		})
	}
	return out
}

// stubServer 起一个 httptest server，go-github 客户端 BaseURL 指向这里
func stubServer(t *testing.T, handler http.Handler) (*RealFetcher, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	f := NewRealFetcher("")
	base, _ := url.Parse(srv.URL + "/")
	f.client.BaseURL = base
	return f, srv.Close
}

func TestRealFetcher_Fetch_Success(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/golang/go/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title":              "fix panic in scanner",
			"body":               "fixes #999",
			"state":              "open",
			"merged":             false,
			"user":               map[string]any{"login": "lin-mei"},
			"author_association": "CONTRIBUTOR",
			"labels":             []map[string]any{{"name": "bug"}, {"name": "needs-review"}},
			"base":               map[string]any{"ref": "main"},
			"head":               map[string]any{"sha": "deadbeef0000", "ref": "fix/scanner-panic"},
			"created_at":         "2026-05-28T10:00:00Z",
			"changed_files":      5,
			"additions":          96,
			"deletions":          41,
			"commits":            4,
			"comments":           7,
		})
	})
	mux.HandleFunc("/repos/golang/go/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"filename":  "src/main.go",
				"status":    "modified",
				"patch":     "@@ -1,1 +1,1 @@\n-old\n+new",
				"additions": 1,
				"deletions": 1,
			},
			{
				"filename":  "README.md",
				"status":    "modified",
				"patch":     "@@ -1 +1 @@\n-A\n+B",
				"additions": 1,
				"deletions": 1,
			},
		})
	})

	f, cleanup := stubServer(t, mux)
	defer cleanup()

	got, err := f.Fetch(context.Background(), "https://github.com/golang/go/pull/42")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if got.Owner != "golang" || got.Repo != "go" || got.Number != 42 {
		t.Errorf("PR 元信息错: %+v", got)
	}
	if got.HeadSHA != "deadbeef0000" {
		t.Errorf("HeadSHA = %q，期望 deadbeef0000", got.HeadSHA)
	}
	if got.Title != "fix panic in scanner" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Body != "fixes #999" {
		t.Errorf("Body = %q", got.Body)
	}
	if len(got.Files) != 2 {
		t.Fatalf("Files 数量 = %d，期望 2", len(got.Files))
	}
	if got.Files[0].Path != "src/main.go" || got.Files[0].Status != "modified" {
		t.Errorf("Files[0] 错: %+v", got.Files[0])
	}
	if got.Files[0].Additions != 1 || got.Files[0].Deletions != 1 {
		t.Errorf("Files[0] 加减行错: +%d -%d", got.Files[0].Additions, got.Files[0].Deletions)
	}
	if got.Files[1].Path != "README.md" {
		t.Errorf("Files[1] 错: %+v", got.Files[1])
	}

	// 新增 meta 字段
	if got.Author != "lin-mei" {
		t.Errorf("Author=%q want lin-mei", got.Author)
	}
	if got.AuthorRole != "CONTRIBUTOR" {
		t.Errorf("AuthorRole=%q want CONTRIBUTOR", got.AuthorRole)
	}
	if got.State != StateOpen {
		t.Errorf("State=%q want open", got.State)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "bug" || got.Labels[1] != "needs-review" {
		t.Errorf("Labels=%v want [bug needs-review]", got.Labels)
	}
	if got.BaseRef != "main" {
		t.Errorf("BaseRef=%q want main", got.BaseRef)
	}
	if got.HeadRef != "fix/scanner-panic" {
		t.Errorf("HeadRef=%q want fix/scanner-panic", got.HeadRef)
	}
	wantTime, _ := time.Parse(time.RFC3339, "2026-05-28T10:00:00Z")
	if !got.CreatedAt.Equal(wantTime) {
		t.Errorf("CreatedAt=%v want %v", got.CreatedAt, wantTime)
	}
	wantStats := Stats{Files: 5, Additions: 96, Deletions: 41, Commits: 4, Comments: 7}
	if got.Stats != wantStats {
		t.Errorf("Stats=%+v want %+v", got.Stats, wantStats)
	}
}

func TestRealFetcher_Fetch_StateMerged(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title":  "x",
			"state":  "closed", // GitHub closed + merged=true → 我们归为 merged
			"merged": true,
			"head":   map[string]any{"sha": "0"},
		})
	})
	mux.HandleFunc("/repos/o/r/pulls/1/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	f, cleanup := stubServer(t, mux)
	defer cleanup()
	got, err := f.Fetch(context.Background(), "https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.State != StateMerged {
		t.Errorf("merged=true 时 State 应为 merged，得到 %q", got.State)
	}
}

func TestRealFetcher_Fetch_ReportsRepoVisibility(t *testing.T) {
	for _, private := range []bool{true, false} {
		mux := http.NewServeMux()
		mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"title": "x",
				"head":  map[string]any{"sha": "0"},
				"base":  map[string]any{"ref": "main", "repo": map[string]any{"private": private}},
			})
		})
		mux.HandleFunc("/repos/o/r/pulls/1/files", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode([]any{})
		})
		f, cleanup := stubServer(t, mux)
		got, err := f.Fetch(context.Background(), "https://github.com/o/r/pull/1")
		cleanup()
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got.Private != private {
			t.Errorf("Private=%v, want %v", got.Private, private)
		}
	}
}

func TestRealFetcher_Fetch_EmptyLabels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title": "no labels",
			"head":  map[string]any{"sha": "0"},
		})
	})
	mux.HandleFunc("/repos/o/r/pulls/1/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	f, cleanup := stubServer(t, mux)
	defer cleanup()
	got, err := f.Fetch(context.Background(), "https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Labels == nil {
		t.Error("Labels 应是空 slice 而非 nil，避免下游 nil-check")
	}
	if len(got.Labels) != 0 {
		t.Errorf("Labels 应为空，得到 %v", got.Labels)
	}
}

func TestRealFetcher_Fetch_InvalidURL(t *testing.T) {
	f := NewRealFetcher("")
	_, err := f.Fetch(context.Background(), "https://gitlab.com/foo/bar/pull/1")
	if !errors.Is(err, ErrInvalidPRURL) {
		t.Errorf("期望 ErrInvalidPRURL，得到 %v", err)
	}
}

func TestRealFetcher_Fetch_PRNotFound(t *testing.T) {
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	})
	f, cleanup := stubServer(t, srv)
	defer cleanup()

	_, err := f.Fetch(context.Background(), "https://github.com/owner/repo/pull/1")
	if !errors.Is(err, ErrPRNotFound) {
		t.Errorf("期望包装 ErrPRNotFound，得到 %v", err)
	}
}

func TestRealFetcher_Fetch_AccessDenied(t *testing.T) {
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"API rate limit exceeded"}`, http.StatusForbidden)
	})
	f, cleanup := stubServer(t, srv)
	defer cleanup()

	_, err := f.Fetch(context.Background(), "https://github.com/owner/repo/pull/1")
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("期望包装 ErrAccessDenied，得到 %v", err)
	}
}

func TestRealFetcher_Fetch_Unauthorized(t *testing.T) {
	srv := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	f, cleanup := stubServer(t, srv)
	defer cleanup()

	_, err := f.Fetch(context.Background(), "https://github.com/owner/repo/pull/1")
	if !errors.Is(err, ErrAccessDenied) {
		t.Errorf("401 也应归类为 ErrAccessDenied，得到 %v", err)
	}
}

func TestRealFetcher_Fetch_AttachesConventions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/golang/go/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title": "x",
			"head":  map[string]any{"sha": "head-sha"},
		})
	})
	mux.HandleFunc("/repos/golang/go/pulls/42/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})
	mux.HandleFunc("/repos/golang/go/contents/README.md", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ref"); got != "head-sha" {
			t.Errorf("contents ref=%q，期望 head-sha", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type":     "file",
			"encoding": "base64",
			"content":  base64.StdEncoding.EncodeToString([]byte("# Go\nuse modules")),
		})
	})
	// CONTRIBUTING.md / CLAUDE.md / AGENTS.md 走 mux 默认 404

	f, cleanup := stubServer(t, mux)
	defer cleanup()

	got, err := f.Fetch(context.Background(), "https://github.com/golang/go/pull/42")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(got.Conventions.Readme, "use modules") {
		t.Errorf("Conventions.Readme 应填充，得到 %q", got.Conventions.Readme)
	}
	if got.Conventions.Contributing != "" || got.Conventions.AgentDocs != "" {
		t.Errorf("缺失文件应留空：%+v", got.Conventions)
	}
}

func TestRealFetcher_Fetch_PaginatesFiles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title": "big PR",
			"head":  map[string]any{"sha": "0"},
		})
	})
	mux.HandleFunc("/repos/o/r/pulls/1/files", func(w http.ResponseWriter, r *http.Request) {
		// 第一页满 100 条 + Link rel=next 指向第二页；第二页 30 条无 Link 终止
		if page := r.URL.Query().Get("page"); page == "" || page == "1" {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=2>; rel="next"`, r.URL.Path))
			_ = json.NewEncoder(w).Encode(makeFiles("p1-", 100))
			return
		}
		_ = json.NewEncoder(w).Encode(makeFiles("p2-", 30))
	})

	f, cleanup := stubServer(t, mux)
	defer cleanup()

	got, err := f.Fetch(context.Background(), "https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Files) != 130 {
		t.Fatalf("Files 数量 = %d，期望 130（分页应跟到第二页）", len(got.Files))
	}
	if got.Files[0].Path != "p1-0.go" || got.Files[129].Path != "p2-29.go" {
		t.Errorf("分页顺序错: 首=%q 尾=%q", got.Files[0].Path, got.Files[129].Path)
	}
}

func TestRealFetcher_Fetch_EmptyFiles(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"title": "empty PR",
			"head":  map[string]any{"sha": "0"},
		})
	})
	mux.HandleFunc("/repos/o/r/pulls/1/files", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]any{})
	})

	f, cleanup := stubServer(t, mux)
	defer cleanup()

	got, err := f.Fetch(context.Background(), "https://github.com/o/r/pull/1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(got.Files) != 0 {
		t.Errorf("期望空 Files，得到 %d 条", len(got.Files))
	}
}
