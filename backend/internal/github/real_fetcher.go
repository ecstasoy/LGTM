package github

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	gh "github.com/google/go-github/v66/github"
)

// maxFilePages 文件列表分页上限；GitHub 单 PR 文件列表本身封顶 3000，30 页兜底防异常分页死循环。
const maxFilePages = 30

// RealFetcher 通过 go-github 调 GitHub REST。
type RealFetcher struct {
	client *gh.Client
}

// NewRealFetcher 构造 RealFetcher
func NewRealFetcher(token string) *RealFetcher {
	c := gh.NewClient(nil)
	if token != "" {
		c = c.WithAuthToken(token)
	}
	return &RealFetcher{client: c}
}

// Fetch 实现 Fetcher。
// 拉 PR meta + 改动文件列表 + 仓库根约定文件。
// meta(Get) 必须先拿到——它提供 HeadSHA；diff / conventions / checks 相互独立，并发拉取。
// Conventions / checks 抓失败降级（warn 日志 + 留空），不阻塞主流程。
func (f *RealFetcher) Fetch(ctx context.Context, rawURL string) (PullRequest, error) {
	owner, repo, number, err := ParseURL(rawURL)
	if err != nil {
		return PullRequest{}, err
	}

	// diff 只依赖 owner/repo/number，先起跑，与下面的 meta(Get) 调用重叠
	var (
		files    []*gh.CommitFile
		filesErr error
		wg       sync.WaitGroup
	)
	wg.Go(func() {
		files, filesErr = fetchFiles(ctx, f.client, owner, repo, number)
	})

	// meta 必须先拿到：HeadSHA 供 conventions / checks 用
	pr, _, err := f.client.PullRequests.Get(ctx, owner, repo, number)
	if err != nil {
		wg.Wait() // 等 files goroutine 收尾再返回，避免泄漏
		return PullRequest{}, fmt.Errorf("get pull request: %w", classifyGitHubError(err))
	}
	headSHA := pr.GetHead().GetSHA()

	// conventions + checks 依赖 HeadSHA，与仍在跑的 files 三者并发
	var (
		conv      Conventions
		convErr   error
		ci        string
		checks    []Check
		checksErr error
	)
	wg.Go(func() {
		conv, convErr = fetchConventions(ctx, f.client, owner, repo, headSHA)
	})
	wg.Go(func() {
		ci, checks, checksErr = fetchChecks(ctx, f.client, owner, repo, headSHA)
	})
	wg.Wait()

	if filesErr != nil {
		return PullRequest{}, fmt.Errorf("list pull request files: %w", classifyGitHubError(filesErr))
	}

	// state 在 GitHub 是 open/closed 二值，merged 单独用 boolean 标识；
	// 这里合并为三态 string 让上层不必再判 merged 标志。
	state := pr.GetState()
	if pr.GetMerged() {
		state = StateMerged
	}

	labels := make([]string, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}

	out := PullRequest{
		Owner:      owner,
		Repo:       repo,
		Number:     number,
		HeadSHA:    headSHA,
		Title:      pr.GetTitle(),
		Body:       pr.GetBody(),
		Private:    pr.GetBase().GetRepo().GetPrivate(),
		Author:     pr.GetUser().GetLogin(),
		AuthorRole: pr.GetAuthorAssociation(),
		State:      state,
		Labels:     labels,
		BaseRef:    pr.GetBase().GetRef(),
		HeadRef:    pr.GetHead().GetRef(),
		CreatedAt:  pr.GetCreatedAt().Time.UTC(),
		Stats: Stats{
			Files:     pr.GetChangedFiles(),
			Additions: pr.GetAdditions(),
			Deletions: pr.GetDeletions(),
			Commits:   pr.GetCommits(),
			Comments:  pr.GetComments(),
		},
	}
	for _, file := range files {
		out.Files = append(out.Files, File{
			Path:      file.GetFilename(),
			Status:    file.GetStatus(),
			Patch:     file.GetPatch(),
			Additions: file.GetAdditions(),
			Deletions: file.GetDeletions(),
		})
	}

	if convErr != nil {
		slog.Warn("fetch conventions failed, continuing without L3",
			"owner", owner, "repo", repo, "err", convErr)
	} else {
		out.Conventions = conv
	}

	// CI 抓失败留空字符串（区分"未知"与"empty=pending"），上层 UI 自行处理"无 CI 信息"显示。
	if checksErr != nil {
		slog.Warn("fetch checks failed, continuing without CI",
			"owner", owner, "repo", repo, "err", checksErr)
	} else {
		out.CI = ci
		out.Checks = checks
	}
	return out, nil
}

// fetchFiles 分页拉全部改动文件；只取第一页会让 >100 文件的大 PR 静默丢文件。
// maxFilePages 兜底防异常分页死循环；超预算裁剪交给 prctx 层（BudgetReport.Dropped 显式上报）。
func fetchFiles(ctx context.Context, client *gh.Client, owner, repo string, number int) ([]*gh.CommitFile, error) {
	var files []*gh.CommitFile
	opt := &gh.ListOptions{PerPage: 100}
	for range maxFilePages {
		batch, resp, err := client.PullRequests.ListFiles(ctx, owner, repo, number, opt)
		if err != nil {
			return nil, err
		}
		files = append(files, batch...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return files, nil
}
