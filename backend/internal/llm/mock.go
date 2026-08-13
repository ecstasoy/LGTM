package llm

import (
	"context"
	"strings"
	"sync"
)

// MockProvider 返回写死的流式响应；LLM_PROVIDER=mock 或缺 key 时启用。
type MockProvider struct {
	Reply string

	mu          sync.Mutex
	lastRequest *Request // captured by Stream; read via LastRequest so tests can assert what was actually sent
}

// NewMockProvider 默认占位回复
func NewMockProvider() *MockProvider {
	return &MockProvider{
		Reply: "# Mock 评审\n\n这是占位回复。设置 LLM_PROVIDER=openai 并配 OPENAI_API_KEY 后切到真实模型。",
	}
}

// Stream 按空白切分逐词推送。
func (m *MockProvider) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	m.mu.Lock()
	reqCopy := req
	m.lastRequest = &reqCopy
	m.mu.Unlock()

	ch := make(chan Chunk, 8)
	go func() {
		defer close(ch)
		for _, word := range strings.Fields(m.Reply) {
			select {
			case <-ctx.Done():
				return
			case ch <- Chunk{Text: word + " "}:
			}
		}
		ch <- Chunk{Done: true}
	}()
	return ch, nil
}

// LastRequest returns the most recent Request passed to Stream, or nil if Stream has not been called yet.
// Safe for concurrent use (guarded by the same mutex Stream writes under) — mergeStages runs stages concurrently
// against provider instances shared across goroutines in some tests.
func (m *MockProvider) LastRequest() *Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRequest
}
