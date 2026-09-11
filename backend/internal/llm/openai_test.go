package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubOpenAIServer 起一个 httptest 模拟 chat completions endpoint。
// handler 收到请求后可读 body 做断言，把要返的 SSE 文本写回。
func stubOpenAIServer(t *testing.T, handler func(t *testing.T, w http.ResponseWriter, body []byte)) *OpenAIProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing or wrong Authorization header")
		}
		body, _ := io.ReadAll(r.Body)
		handler(t, w, body)
	}))
	t.Cleanup(srv.Close)
	return NewOpenAIProvider(srv.URL, "test-key", "test-model")
}

// sseLine 拼一行 SSE。
func sseLine(payload string) string { return "data: " + payload + "\n\n" }

func TestOpenAIProvider_Stream_Success(t *testing.T) {
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		var req openAIChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("解析请求体: %v", err)
		}
		if !req.Stream {
			t.Errorf("期望 stream=true")
		}
		if req.Model != "test-model" {
			t.Errorf("model = %q，期望 test-model", req.Model)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Errorf("messages 错: %+v", req.Messages)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		// 三段 delta + DONE
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"Hello"}}]}`))
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":" world"}}]}`))
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"!"}}]}`))
		fmt.Fprint(w, sseLine("[DONE]"))
	})

	ch, err := p.Stream(context.Background(), Request{System: "你是助手", User: "hi"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var collected strings.Builder
	var done bool
	for c := range ch {
		if c.Err != nil {
			t.Fatalf("意外 chunk err: %v", c.Err)
		}
		if c.Done {
			done = true
			continue
		}
		collected.WriteString(c.Text)
	}
	if got := collected.String(); got != "Hello world!" {
		t.Errorf("拼接结果 = %q，期望 \"Hello world!\"", got)
	}
	if !done {
		t.Errorf("期望收到 Done chunk")
	}
}

func TestOpenAIProvider_Stream_Non200(t *testing.T) {
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		http.Error(w, `{"error":{"message":"key invalid"}}`, http.StatusUnauthorized)
	})

	_, err := p.Stream(context.Background(), Request{User: "hi"})
	if err == nil {
		t.Fatal("期望错误")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("错误信息应含状态码 401，实际: %v", err)
	}
}

func TestOpenAIProvider_Stream_JSONSchemaMode(t *testing.T) {
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		var req openAIChatRequest
		_ = json.Unmarshal(body, &req)
		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Errorf("期望 response_format.type=json_object，实际 %+v", req.ResponseFormat)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"{}"}}]}`))
		fmt.Fprint(w, sseLine("[DONE]"))
	})

	ch, err := p.Stream(context.Background(), Request{
		User:       "请输出 JSON",
		JSONSchema: &Schema{Name: "Risks"},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	} // 排空，让 goroutine 收尾
}

func TestOpenAIProvider_Stream_ContextCancel(t *testing.T) {
	// 服务端慢慢推，给 client 取消的机会
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"tick "}}]}`))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(50 * time.Millisecond)
		}
		fmt.Fprint(w, sseLine("[DONE]"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := p.Stream(ctx, Request{User: "hi"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	<-ch // 收到第一帧后取消
	cancel()

	// 期望 goroutine 在 ctx 取消后退出，channel 最终 close
	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // 正常 close
			}
		case <-deadline:
			t.Fatal("ctx 取消后 channel 未在 2s 内关闭")
		}
	}
}

// stream tool_calls 跨多帧分片，验证按 index 累积并 emit Chunk{ToolCalls}
func TestOpenAIProvider_Stream_ToolCalls_AccumulatedAcrossDeltas(t *testing.T) {
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		var req openAIChatRequest
		_ = json.Unmarshal(body, &req)
		if len(req.Tools) != 1 || req.Tools[0].Function.Name != "read_file" {
			t.Errorf("expected tools[0].function.name=read_file, got %+v", req.Tools)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// 模拟 OpenAI 跨帧分片：第 1 帧建立 id/name + 部分 args，第 2 帧补 args 余下，第 3 帧 finish_reason
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"read_file","arguments":"{\"file\":"}}]}}]}`))
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"main.go\"}"}}]}}]}`))
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`))
		fmt.Fprint(w, sseLine("[DONE]"))
	})

	schema := json.RawMessage(`{"type":"object"}`)
	ch, err := p.Stream(context.Background(), Request{
		User:  "evaluate this PR",
		Tools: []ToolSpec{{Name: "read_file", Description: "read a file", Parameters: schema}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var (
		gotCalls []ToolCall
		sawDone  bool
	)
	for c := range ch {
		if c.Err != nil {
			t.Fatalf("chunk err: %v", c.Err)
		}
		if len(c.ToolCalls) > 0 {
			gotCalls = c.ToolCalls
		}
		if c.Done {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatal("没收到 Done")
	}
	if len(gotCalls) != 1 {
		t.Fatalf("want 1 tool_call, got %d: %+v", len(gotCalls), gotCalls)
	}
	got := gotCalls[0]
	if got.ID != "call_abc" || got.Name != "read_file" || got.Arguments != `{"file":"main.go"}` {
		t.Errorf("tool call mismatch: %+v", got)
	}
}

// Messages 多轮 + tool 角色回灌
func TestOpenAIProvider_Stream_MessagesAndToolRoleRoundTrip(t *testing.T) {
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		var req openAIChatRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		// 3 条消息：user / assistant(tool_calls) / tool(result)
		if len(req.Messages) != 3 {
			t.Fatalf("want 3 messages, got %d", len(req.Messages))
		}
		if req.Messages[1].Role != "assistant" || len(req.Messages[1].ToolCalls) != 1 {
			t.Errorf("msg[1] should be assistant with tool_calls, got %+v", req.Messages[1])
		}
		if req.Messages[2].Role != "tool" || req.Messages[2].ToolCallID != "call_abc" {
			t.Errorf("msg[2] should be tool with call_id, got %+v", req.Messages[2])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"done"}}]}`))
		fmt.Fprint(w, sseLine("[DONE]"))
	})

	ch, err := p.Stream(context.Background(), Request{
		Messages: []Message{
			{Role: "user", Content: "evaluate"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_abc", Name: "read_file", Arguments: `{"file":"main.go"}`}}},
			{Role: "tool", ToolCallID: "call_abc", Name: "read_file", Content: "package main\n..."},
		},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var text strings.Builder
	for c := range ch {
		text.WriteString(c.Text)
	}
	if text.String() != "done" {
		t.Errorf("final text=%q want done", text.String())
	}
}

// recordWaits swaps in a wait that records durations instead of sleeping.
func recordWaits(p *OpenAIProvider) *[]time.Duration {
	var waits []time.Duration
	p.wait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	return &waits
}

// drainText collects stream text, failing on any chunk error.
func drainText(t *testing.T, ch <-chan Chunk) string {
	t.Helper()
	var b strings.Builder
	for c := range ch {
		if c.Err != nil {
			t.Fatalf("chunk err: %v", c.Err)
		}
		b.WriteString(c.Text)
	}
	return b.String()
}

func TestOpenAIProvider_Stream_RetriesTransientStatusThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		attempts.Add(1)
		var req openAIChatRequest
		if err := json.Unmarshal(body, &req); err != nil || req.Model != "test-model" {
			t.Errorf("attempt %d: request body must be resent intact, got err=%v body=%q", attempts.Load(), err, body)
		}
		if attempts.Load() < 3 {
			http.Error(w, `{"error":{"message":"overloaded"}}`, http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"ok"}}]}`))
		fmt.Fprint(w, sseLine("[DONE]"))
	})
	waits := recordWaits(p)

	ch, err := p.Stream(context.Background(), Request{User: "hi"})
	if err != nil {
		t.Fatalf("Stream should succeed on the 3rd attempt: %v", err)
	}
	if got := drainText(t, ch); got != "ok" {
		t.Errorf("text=%q want ok", got)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts=%d want 3", attempts.Load())
	}
	if len(*waits) != 2 {
		t.Fatalf("want 2 backoff waits, got %v", *waits)
	}
	// equal jitter on a 500ms base: 1st wait in [250ms, 500ms], 2nd in [500ms, 1s]
	if w := (*waits)[0]; w < 250*time.Millisecond || w > 500*time.Millisecond {
		t.Errorf("1st wait=%v want within [250ms, 500ms]", w)
	}
	if w := (*waits)[1]; w < 500*time.Millisecond || w > time.Second {
		t.Errorf("2nd wait=%v want within [500ms, 1s]", w)
	}
}

func TestOpenAIProvider_Stream_HonorsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		attempts.Add(1)
		if attempts.Load() == 1 {
			w.Header().Set("Retry-After", "7")
			http.Error(w, `{"error":{"message":"rate limited"}}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseLine("[DONE]"))
	})
	waits := recordWaits(p)

	ch, err := p.Stream(context.Background(), Request{User: "hi"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drainText(t, ch)
	if len(*waits) != 1 || (*waits)[0] != 7*time.Second {
		t.Errorf("want a single 7s wait from Retry-After, got %v", *waits)
	}
}

func TestOpenAIProvider_Stream_CapsHugeRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		attempts.Add(1)
		if attempts.Load() == 1 {
			w.Header().Set("Retry-After", "3600")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseLine("[DONE]"))
	})
	waits := recordWaits(p)

	ch, err := p.Stream(context.Background(), Request{User: "hi"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	drainText(t, ch)
	if len(*waits) != 1 || (*waits)[0] <= 0 || (*waits)[0] > time.Minute {
		t.Errorf("an hour-long Retry-After must not stall the request; want one wait in (0, 1m], got %v", *waits)
	}
}

func TestOpenAIProvider_Stream_GivesUpAfterThreeAttempts(t *testing.T) {
	var attempts atomic.Int32
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		attempts.Add(1)
		http.Error(w, `{"error":{"message":"down"}}`, http.StatusBadGateway)
	})
	recordWaits(p)

	_, err := p.Stream(context.Background(), Request{User: "hi"})
	if err == nil {
		t.Fatal("want an error after exhausting retries")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should carry the last status 502, got %v", err)
	}
	if attempts.Load() != 3 {
		t.Errorf("attempts=%d want 3", attempts.Load())
	}
}

func TestOpenAIProvider_Stream_DoesNotRetryClientErrors(t *testing.T) {
	var attempts atomic.Int32
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		attempts.Add(1)
		http.Error(w, `{"error":{"message":"key invalid"}}`, http.StatusUnauthorized)
	})
	waits := recordWaits(p)

	if _, err := p.Stream(context.Background(), Request{User: "hi"}); err == nil {
		t.Fatal("want error")
	}
	if attempts.Load() != 1 || len(*waits) != 0 {
		t.Errorf("a 401 won't fix itself; want 1 attempt and no waits, got attempts=%d waits=%v", attempts.Load(), *waits)
	}
}

// failFirstTransport fails the first n round trips with a connection error, then delegates.
type failFirstTransport struct {
	n     int
	calls int
}

func (f *failFirstTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.calls++
	if f.calls <= f.n {
		return nil, errors.New("connection reset by peer")
	}
	return http.DefaultTransport.RoundTrip(r)
}

func TestOpenAIProvider_Stream_RetriesConnectionErrors(t *testing.T) {
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"ok"}}]}`))
		fmt.Fprint(w, sseLine("[DONE]"))
	})
	tr := &failFirstTransport{n: 1}
	p.HTTPClient = &http.Client{Transport: tr}
	waits := recordWaits(p)

	ch, err := p.Stream(context.Background(), Request{User: "hi"})
	if err != nil {
		t.Fatalf("a single dropped connection should be retried: %v", err)
	}
	if got := drainText(t, ch); got != "ok" {
		t.Errorf("text=%q want ok", got)
	}
	if tr.calls != 2 || len(*waits) != 1 {
		t.Errorf("want 2 round trips and 1 wait, got calls=%d waits=%v", tr.calls, *waits)
	}
}

func TestOpenAIProvider_Stream_DoesNotRetryAfterContextCanceled(t *testing.T) {
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {})
	waits := recordWaits(p)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Stream(ctx, Request{User: "hi"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if len(*waits) != 0 {
		t.Errorf("a caller that gave up must not be retried; got waits=%v", *waits)
	}
}

func TestOpenAIProvider_Stream_CancelDuringBackoffReturnsPromptly(t *testing.T) {
	var attempts atomic.Int32
	p := stubOpenAIServer(t, func(t *testing.T, w http.ResponseWriter, body []byte) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "30")
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := p.Stream(ctx, Request{User: "hi"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("backoff must end when the caller gives up; took %v", elapsed)
	}
	if attempts.Load() != 1 {
		t.Errorf("attempts=%d want 1", attempts.Load())
	}
}

func TestStreamingClient_TimesOutWaitingForHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	start := time.Now()
	resp, err := newStreamingClient(50 * time.Millisecond).Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("want a timeout while the server withholds response headers; returned after %v", time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("header timeout should fire near 50ms, took %v", elapsed)
	}
}

func TestStreamingClient_StreamMayOutlastHeaderTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f := w.(http.Flusher)
		f.Flush()
		for i := 0; i < 4; i++ {
			time.Sleep(50 * time.Millisecond)
			fmt.Fprint(w, sseLine(`{"choices":[{"delta":{"content":"x"}}]}`))
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	resp, err := newStreamingClient(50 * time.Millisecond).Get(srv.URL)
	if err != nil {
		t.Fatalf("headers arrive immediately, so no timeout expected: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("a 200ms stream must not be cut off by a 50ms header timeout: %v", err)
	}
	if got := strings.Count(string(body), "data: "); got != 4 {
		t.Errorf("want all 4 events, got %d", got)
	}
}
