package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// GetPullHeadSHA returns the PR's current head commit, so a finished review can tell whether it is already stale.
func (c *Client) GetPullHeadSHA(ctx context.Context, accessToken, owner, repo string, number int) (string, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls/%d", owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("oauth: build pull req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("oauth: get pull: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("oauth: get pull %d: %s", res.StatusCode, string(body))
	}
	var p struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return "", fmt.Errorf("oauth: parse pull: %w", err)
	}
	return p.Head.SHA, nil
}
