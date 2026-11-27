package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// Comment GitHub `/pulls/{n}/comments` 返回的 review comment 字段子集
// HTMLUrl 让前端给用户跳到 GitHub 上看新发的 comment
// NodeID 给后续 G6c GraphQL 的 applyPullRequestReviewThreadSuggestion 用（需要 thread node id，但 comment 创建后可查）
type Comment struct {
	ID      int64  `json:"id"`
	NodeID  string `json:"node_id"`
	HTMLUrl string `json:"html_url"`
	Body    string `json:"body"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
}

// PostPRComment posts one PR review comment; the caller builds any ```suggestion block into body.
// commitID (head SHA), path and line are required; side is RIGHT (new code).
// startLine > 0 makes it a multi-line comment over startLine..line, which a multi-line suggestion block replaces.
//
// 文档 https://docs.github.com/rest/pulls/comments#create-a-review-comment-for-a-pull-request
// 权限 pull_requests:write
func (c *Client) PostPRComment(
	ctx context.Context,
	accessToken, owner, repo string,
	prNumber int,
	body, commitID, path string,
	startLine, line int,
) (*Comment, error) {
	payload := map[string]any{
		"body":      body,
		"commit_id": commitID,
		"path":      path,
		"line":      line,
		"side":      "RIGHT",
	}
	if startLine > 0 && startLine < line {
		payload["start_line"] = startLine
		payload["start_side"] = "RIGHT"
	}
	raw, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/comments", owner, repo, prNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("oauth: build comment req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: post comment: %w", err)
	}
	defer res.Body.Close()
	rawResp, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("oauth: comment %d: %s", res.StatusCode, string(rawResp))
	}
	var cm Comment
	if err := json.Unmarshal(rawResp, &cm); err != nil {
		return nil, fmt.Errorf("oauth: parse comment resp: %w", err)
	}
	return &cm, nil
}

// PostIssueComment PR 级别评论（非 inline）；slash command 的"已收到"ack 用
// 文档 https://docs.github.com/rest/issues/comments#create-an-issue-comment
// PR 在 GitHub 也算 Issue，所以这个 endpoint 适用 PR
func (c *Client) PostIssueComment(ctx context.Context, accessToken, owner, repo string, issueNumber int, body string) (*Comment, error) {
	payload := map[string]any{"body": body}
	raw, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/issues/%d/comments", owner, repo, issueNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("oauth: build issue comment: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: post issue comment: %w", err)
	}
	defer res.Body.Close()
	rawResp, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("oauth: issue comment %d: %s", res.StatusCode, string(rawResp))
	}
	var cm Comment
	if err := json.Unmarshal(rawResp, &cm); err != nil {
		return nil, fmt.Errorf("oauth: parse issue comment: %w", err)
	}
	return &cm, nil
}

// DeletePRComment 撤回一条 PR review comment（用户后悔点了 💬 评论 → 还能删）
// 文档 https://docs.github.com/rest/pulls/comments#delete-a-review-comment-for-a-pull-request
// 权限：comment 作者本人或 maintainer 才能删
// 404 当成"已被删"返 nil 让 caller 视作幂等
func (c *Client) DeletePRComment(ctx context.Context, accessToken, owner, repo string, commentID int64) error {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/comments/%d", owner, repo, commentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("oauth: build delete req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("oauth: delete comment: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	switch res.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusNotFound:
		// 已被删 → 幂等
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("oauth: delete comment forbidden (%d): %s", res.StatusCode, string(body))
	default:
		return fmt.Errorf("oauth: delete comment %d: %s", res.StatusCode, string(body))
	}
}
