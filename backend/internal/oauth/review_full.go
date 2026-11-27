package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ReviewCommentInline is one inline comment submitted with a review, on line Line (or StartLine..Line) of Path.
type ReviewCommentInline struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"` // 0 means a single-line comment on Line.
	StartSide string `json:"start_side,omitempty"` // Defaults to RIGHT when StartLine is set.
	Line      int    `json:"line"`
	Side      string `json:"side"` // Defaults to RIGHT.
	Body      string `json:"body"`
}

// PostPRReview 一次性提交完整 PR review（带 summary body + N 条 inline 评论）
// 用 bot installation token 调（caller 准备）→ comment 作者 = lgtm-ai-reviewer[bot]
//
// event = "COMMENT" 不算审批；"APPROVE" / "REQUEST_CHANGES" 是带评审决议的
// 我们用 "COMMENT" 因为是 AI 建议不该自动 approve
//
// 文档 https://docs.github.com/rest/pulls/reviews#create-a-review-for-a-pull-request
func (c *Client) PostPRReview(
	ctx context.Context,
	accessToken, owner, repo string,
	prNumber int,
	commitID, body string,
	inline []ReviewCommentInline,
) (*PRReview, error) {
	for i := range inline {
		if inline[i].Side == "" {
			inline[i].Side = "RIGHT"
		}
		if inline[i].StartLine <= 0 || inline[i].StartLine >= inline[i].Line {
			inline[i].StartLine, inline[i].StartSide = 0, ""
		} else if inline[i].StartSide == "" {
			inline[i].StartSide = "RIGHT"
		}
	}
	payload := map[string]any{
		"commit_id": commitID,
		"body":      body,
		"event":     "COMMENT",
		"comments":  inline,
	}
	raw, _ := json.Marshal(payload)
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d/reviews", owner, repo, prNumber)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("oauth: build review req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: post review: %w", err)
	}
	defer res.Body.Close()
	rawResp, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: review %d: %s", res.StatusCode, string(rawResp))
	}
	var r PRReview
	if err := json.Unmarshal(rawResp, &r); err != nil {
		return nil, fmt.Errorf("oauth: parse review resp: %w", err)
	}
	return &r, nil
}

// PRReview /pulls/{n}/reviews 返字段子集
type PRReview struct {
	ID      int64  `json:"id"`
	NodeID  string `json:"node_id"`
	HTMLUrl string `json:"html_url"`
	State   string `json:"state"`
	Body    string `json:"body"`
}
