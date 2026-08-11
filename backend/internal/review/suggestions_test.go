package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// drainSuggestions 收完所有 event；返 suggestions / error message / done 标记。
func drainSuggestions(t *testing.T, ch <-chan Event) (suggestions []Suggestion, errMsg string, doneSeen bool) {
	t.Helper()
	for ev := range ch {
		switch ev.Type {
		case "suggestions_done":
			if err := json.Unmarshal(ev.Data, &suggestions); err != nil {
				t.Fatalf("unmarshal suggestions_done: %v", err)
			}
		case "error":
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(ev.Data, &p)
			errMsg = p.Message
		case "done":
			doneSeen = true
		}
	}
	return
}

func TestSuggestionsStage_Run_WithPatch(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = `{"suggestions":[{"file":"main.go","line":42,"type":"bug","title":"加锁防竞态","body":"在写 s.items 前加 s.mu.Lock()","patch":{"lang":"go","before":"s.items[k] = v","after":"s.mu.Lock()\ns.items[k] = v\ns.mu.Unlock()"}}]}`

	ch, err := SuggestionsStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	suggestions, errMsg, doneSeen := drainSuggestions(t, ch)

	if errMsg != "" {
		t.Fatalf("意外 error: %s", errMsg)
	}
	if !doneSeen {
		t.Error("缺 done event")
	}
	if len(suggestions) != 1 {
		t.Fatalf("suggestions 数量 = %d，期望 1", len(suggestions))
	}
	s := suggestions[0]
	if s.File != "main.go" || s.Line != 42 || s.Type != "bug" {
		t.Errorf("基本字段错: %+v", s)
	}
	if s.Title == "" || s.Body == "" {
		t.Errorf("title/body 为空: %+v", s)
	}
	if s.Patch == nil {
		t.Fatal("patch 应非 nil")
	}
	if s.Patch.Lang != "go" || !strings.Contains(s.Patch.After, "Lock") {
		t.Errorf("patch 字段错: %+v", s.Patch)
	}
}

func TestSuggestionsStage_Run_WithoutPatch(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = `{"suggestions":[{"file":"util.go","line":10,"type":"style","title":"重命名变量","body":"x 改成 idx 更清晰","patch":null}]}`

	ch, err := SuggestionsStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	suggestions, errMsg, doneSeen := drainSuggestions(t, ch)

	if errMsg != "" {
		t.Fatalf("意外 error: %s", errMsg)
	}
	if !doneSeen {
		t.Error("缺 done")
	}
	if len(suggestions) != 1 {
		t.Fatalf("suggestions 数量 = %d，期望 1", len(suggestions))
	}
	if suggestions[0].Patch != nil {
		t.Errorf("patch 应为 nil，得到 %+v", suggestions[0].Patch)
	}
}

func TestSuggestionsStage_Run_Empty(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = `{"suggestions":[]}`

	ch, err := SuggestionsStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	suggestions, errMsg, doneSeen := drainSuggestions(t, ch)

	if errMsg != "" {
		t.Fatalf("意外 error: %s", errMsg)
	}
	if len(suggestions) != 0 {
		t.Errorf("应为空，得到 %d 条", len(suggestions))
	}
	if !doneSeen {
		t.Error("缺 done")
	}
}

func TestSuggestionsStage_Run_MalformedJSON(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = "not json"

	ch, err := SuggestionsStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, errMsg, doneSeen := drainSuggestions(t, ch)

	if errMsg == "" {
		t.Fatal("应触发 error event")
	}
	if !strings.Contains(errMsg, "parse suggestions JSON") {
		t.Errorf("错误信息应含 parse suggestions JSON，实际: %s", errMsg)
	}
	if doneSeen {
		t.Error("解析失败不应再 emit done")
	}
}

func TestSuggestionsStage_Run_MissingArray(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = `{}`

	ch, err := SuggestionsStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, errMsg, doneSeen := drainSuggestions(t, ch)

	if errMsg == "" {
		t.Fatal("应触发 error event：LLM 返 {} 算协议违反")
	}
	if !strings.Contains(errMsg, "missing suggestions array") {
		t.Errorf("错误信息应含 missing suggestions array，实际: %s", errMsg)
	}
	if doneSeen {
		t.Error("协议违反不应再 emit done")
	}
}

func TestSuggestionsStage_Run_StreamError(t *testing.T) {
	_, err := SuggestionsStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, errProvider{err: streamErr{msg: "stream failed"}})
	if err == nil {
		t.Fatal("期望同步 Stream 错误向上冒")
	}
	if !strings.Contains(err.Error(), "stream") {
		t.Errorf("错误信息应含 stream，得到 %v", err)
	}
}
