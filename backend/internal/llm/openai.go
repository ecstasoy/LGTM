package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OpenAIProvider 调 OpenAI 兼容的 /v1/chat/completions
type OpenAIProvider struct {
	BaseURL string
	APIKey  string
	Model   string

	HTTPClient *http.Client // 默认 http.DefaultClient

	// wait sleeps between retry attempts; nil means a context-aware timer. Swapped in tests.
	wait func(ctx context.Context, d time.Duration) error
}

// NewOpenAIProvider 构造器
func NewOpenAIProvider(baseURL, apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{BaseURL: baseURL, APIKey: apiKey, Model: model}
}

// Stream 以 stream=true 发起 chat completion，按 SSE 推送 delta。
// 支持 function calling：Request.Tools 非空时把 tools 传给 OpenAI，
// 收到 tool_calls 累积完成后在 Done 帧之前 emit 一帧 Chunk{ToolCalls: [...]}。
func (p *OpenAIProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	body, err := buildRequestBody(req, p.Model)
	if err != nil {
		return nil, err
	}

	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := p.connect(ctx, client, body)
	if err != nil {
		return nil, err
	}

	ch := make(chan Chunk, 16)
	go streamSSE(ctx, resp.Body, ch)
	return ch, nil
}

const (
	maxAttempts   = 3
	backoffBase   = 500 * time.Millisecond
	maxRetryAfter = 30 * time.Second
)

// connect posts the request until it gets a 200, retrying transient statuses; a stream that has started is never retried.
func (p *OpenAIProvider) connect(ctx context.Context, client *http.Client, body []byte) (*http.Response, error) {
	endpoint := strings.TrimRight(p.BaseURL, "/") + "/v1/chat/completions"
	for attempt := 1; ; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
		httpReq.Header.Set("Accept", "text/event-stream")

		resp, err := client.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("post chat completions: %w", err)
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if !retryableStatus(resp.StatusCode) || attempt == maxAttempts {
			if attempt > 1 {
				return nil, fmt.Errorf("chat completions: status %d after %d attempts: %s", resp.StatusCode, attempt, string(b))
			}
			return nil, fmt.Errorf("chat completions: status %d: %s", resp.StatusCode, string(b))
		}
		if err := p.sleep(ctx, retryDelay(attempt, resp.Header.Get("Retry-After"))); err != nil {
			return nil, err
		}
	}
}

func retryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// retryDelay honors a seconds-form Retry-After (capped), otherwise equal-jitter exponential backoff.
func retryDelay(attempt int, retryAfter string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxRetryAfter)
	}
	half := (backoffBase << (attempt - 1)) / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// sleep waits d or until ctx is done.
func (p *OpenAIProvider) sleep(ctx context.Context, d time.Duration) error {
	if p.wait != nil {
		return p.wait(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// streamSSE 按行扫描 SSE body，解析 `data: {...}` 推到 ch。
// 在 ctx 取消 / `[DONE]` / EOF 时退出并 close(ch)。
//
// Tool calls 累积逻辑：OpenAI 流式按 index 分片传 tool_calls；
// 每个 index 累 id/name/arguments 字符串，到 finish_reason="tool_calls" 或 [DONE] 时
// 整理成完整 ToolCall 列表 emit 一帧（不增量推，避免前端解析半截 JSON）。
func streamSSE(ctx context.Context, body io.ReadCloser, ch chan<- Chunk) {
	defer body.Close()
	defer close(ch)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 4096), 1<<20)

	// 按 index 累积 tool_calls；OpenAI 协议 index 是稳定整数键
	type partialCall struct {
		id        string
		name      string
		arguments strings.Builder
	}
	partials := map[int]*partialCall{}
	var sawToolCalls bool

	flushToolCalls := func() {
		if !sawToolCalls || len(partials) == 0 {
			return
		}
		idxs := make([]int, 0, len(partials))
		for k := range partials {
			idxs = append(idxs, k)
		}
		sort.Ints(idxs)
		calls := make([]ToolCall, 0, len(idxs))
		for _, i := range idxs {
			p := partials[i]
			calls = append(calls, ToolCall{
				ID:        p.id,
				Name:      p.name,
				Arguments: p.arguments.String(),
			})
		}
		select {
		case <-ctx.Done():
		case ch <- Chunk{ToolCalls: calls}:
		}
		partials = map[int]*partialCall{}
		sawToolCalls = false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			flushToolCalls()
			select {
			case <-ctx.Done():
			case ch <- Chunk{Done: true}:
			}
			return
		}

		var delta openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &delta); err != nil {
			continue // 跳过非法 JSON
		}
		for _, choice := range delta.Choices {
			// content delta
			if t := choice.Delta.Content; t != "" {
				select {
				case <-ctx.Done():
					return
				case ch <- Chunk{Text: t}:
				}
			}
			// tool_calls delta：按 index 累积
			for _, tc := range choice.Delta.ToolCalls {
				sawToolCalls = true
				p, ok := partials[tc.Index]
				if !ok {
					p = &partialCall{}
					partials[tc.Index] = p
				}
				if tc.ID != "" {
					p.id = tc.ID
				}
				if tc.Function.Name != "" {
					p.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					p.arguments.WriteString(tc.Function.Arguments)
				}
			}
			// finish_reason="tool_calls" → 本轮聚合完，提前 flush（仍等 [DONE] 终止流）
			if choice.FinishReason == "tool_calls" {
				flushToolCalls()
			}
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-ctx.Done():
		case ch <- Chunk{Err: err}:
		}
	}
}

// buildRequestBody 拼接 chat completions 请求体 JSON。
// Messages 非空时用它；否则回退 System+User 单轮兼容 v1/v2 stage 调用。
func buildRequestBody(req Request, defaultModel string) ([]byte, error) {
	model := req.Model
	if model == "" {
		model = defaultModel
	}

	var msgs []openAIMessage
	if len(req.Messages) > 0 {
		msgs = make([]openAIMessage, 0, len(req.Messages))
		for _, m := range req.Messages {
			om := openAIMessage{Role: m.Role, Content: m.Content, Name: m.Name, ToolCallID: m.ToolCallID}
			for _, tc := range m.ToolCalls {
				om.ToolCalls = append(om.ToolCalls, openAIToolCall{
					ID:   tc.ID,
					Type: "function",
					Function: openAIFunctionCall{
						Name:      tc.Name,
						Arguments: tc.Arguments,
					},
				})
			}
			msgs = append(msgs, om)
		}
	} else {
		msgs = make([]openAIMessage, 0, 2)
		if req.System != "" {
			msgs = append(msgs, openAIMessage{Role: "system", Content: req.System})
		}
		if req.User != "" {
			msgs = append(msgs, openAIMessage{Role: "user", Content: req.User})
		}
	}

	body := openAIChatRequest{
		Model:       model,
		Messages:    msgs,
		Temperature: req.Temperature,
		Stream:      true,
	}
	// Tools 优先于 JSONSchema（function calling 自带结构化）
	if len(req.Tools) > 0 {
		body.Tools = make([]openAITool, 0, len(req.Tools))
		for _, t := range req.Tools {
			body.Tools = append(body.Tools, openAITool{
				Type: "function",
				Function: openAIFunctionDef{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  t.Parameters,
				},
			})
		}
	} else if req.JSONSchema != nil {
		body.ResponseFormat = &openAIResponseFormat{Type: "json_object"}
	}
	return json.Marshal(body)
}

// OpenAI chat completions 请求 / 响应类型
// 一组放一起，方便维护

type openAIChatRequest struct {
	Model          string                `json:"model"`
	Messages       []openAIMessage       `json:"messages"`
	Temperature    float32               `json:"temperature,omitempty"`
	Stream         bool                  `json:"stream"`
	Tools          []openAITool          `json:"tools,omitempty"`
	ResponseFormat *openAIResponseFormat `json:"response_format,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAIResponseFormat struct {
	Type string `json:"type"`
}

type openAITool struct {
	Type     string            `json:"type"` // "function"
	Function openAIFunctionDef `json:"function"`
}

type openAIFunctionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"` // "function"
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"` // raw JSON string
}

// streaming SSE delta
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string                  `json:"content,omitempty"`
			ToolCalls []openAIDeltaToolCall   `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
}

type openAIDeltaToolCall struct {
	Index    int                `json:"index"`
	ID       string             `json:"id,omitempty"`
	Type     string             `json:"type,omitempty"`
	Function openAIFunctionCall `json:"function"`
}
