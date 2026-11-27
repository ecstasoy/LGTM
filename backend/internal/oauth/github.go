// Package oauth GitHub OAuth user-to-server flow（GitHub App 模式，不是 OAuth App）。
//
// 流程：
//  1. AuthorizeURL(state) → 拼授权页 URL；handler 用 302 引导
//  2. 用户在 github.com 上同意 → 重定向回 redirect_uri 带 ?code=&state=
//  3. ExchangeCode(code) → 用 client_id + client_secret + code 换 user access_token
//  4. FetchUser(token) → 拿登录用户 id + login
//
// 与 GitHub OAuth App 唯一差异：GitHub App 不传 scope（权限在 App settings 配死）
// 参考 https://docs.github.com/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-user-access-token-for-a-github-app
package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// authorizeURL GitHub 授权页；用户在这里"同意"
	authorizeURL = "https://github.com/login/oauth/authorize"
	// tokenURL 用 code 换 user access_token
	tokenURL = "https://github.com/login/oauth/access_token"
	// userAPI 拿登录用户基础信息
	userAPI = "https://api.github.com/user"
)

// User GitHub /user API 字段子集；只取需要的
type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	AvatarURL string `json:"avatar_url"`
	Name      string `json:"name"`
}

// TokenResponse /login/oauth/access_token 返回字段
// expires_in 可选：GitHub App 启用 token expiration 时才有值；否则 token 不过期
type TokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	Scope                 string `json:"scope"`
	ExpiresIn             int    `json:"expires_in,omitempty"`              // 秒
	RefreshToken          string `json:"refresh_token,omitempty"`           // 启用 expiration 才返
	RefreshTokenExpiresIn int    `json:"refresh_token_expires_in,omitempty"`
}

// Client GitHub OAuth + App 客户端；不持 state，handler 自行管 state 防 CSRF
// 字段分两组：
//   - ClientID/Secret/RedirectURI: OAuth web flow（用户登录）
//   - AppID/PrivateKeyPEM: GitHub App JWT 签名（webhook → installation token → bot 身份调 GitHub）
type Client struct {
	ClientID     string
	ClientSecret string
	// RedirectURI 必须跟 GitHub App settings 里 Callback URL 完全一致
	// e.g. https://lgtm.example.com/api/auth/github/callback (PUBLIC_BASE_URL + the callback path by default)
	RedirectURI string

	// AppID + PrivateKeyPEM 用于 webhook 路径换 installation token（bot 身份发评审）
	// 没配时 webhook 仍能验签 + 触发评审，但无法 post bot review 回 PR；仅在 lgtm.com 多一条
	AppID         int64
	PrivateKeyPEM []byte

	// HTTPClient 可选；nil 时用 default + 10s 超时
	HTTPClient *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// AuthorizeURL 拼授权页 URL；state 由 caller 传（通常是随机串塞 cookie 做 CSRF 校验）
// 注意 GitHub App 不传 scope —— 权限以 App permissions 为准
func (c *Client) AuthorizeURL(state string) string {
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURI)
	q.Set("state", state)
	return authorizeURL + "?" + q.Encode()
}

// ExchangeCode 用授权码换 user access_token；code 单次有效，10 分钟内换
func (c *Client) ExchangeCode(ctx context.Context, code string) (*TokenResponse, error) {
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", c.RedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("oauth: build token req: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: token exchange: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: token endpoint %d: %s", res.StatusCode, string(body))
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("oauth: parse token: %w (body=%s)", err, string(body))
	}
	if tok.AccessToken == "" {
		// GitHub 偶尔在 200 里返 error 字段（如 bad_verification_code）
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(body, &errResp)
		return nil, fmt.Errorf("oauth: empty access_token (error=%s, desc=%s)", errResp.Error, errResp.ErrorDescription)
	}
	return &tok, nil
}

// FetchUser 用 user access_token 调 /user API 拿 id + login
func (c *Client) FetchUser(ctx context.Context, accessToken string) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userAPI, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth: build user req: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth: fetch user: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oauth: /user %d: %s", res.StatusCode, string(body))
	}
	var u User
	if err := json.Unmarshal(body, &u); err != nil {
		return nil, fmt.Errorf("oauth: parse user: %w", err)
	}
	return &u, nil
}
