package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/llm"
	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// drainRisks 收完所有 event；返回 risks_done payload（若有）和 error message（若有）。
func drainRisks(t *testing.T, ch <-chan Event) (risks []Risk, errMsg string, doneSeen bool) {
	t.Helper()
	for ev := range ch {
		switch ev.Type {
		case "risks_done":
			if err := json.Unmarshal(ev.Data, &risks); err != nil {
				t.Fatalf("unmarshal risks_done: %v", err)
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

func TestRisksStage_Run_Success(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = `{"risks":[{"file":"main.go","line":42,"severity":"high","category":"bug","confidence":0.95,"reason":"空指针解引用"},{"file":"util.go","severity":"low","category":"style","confidence":0.6,"reason":"命名不清"}]}`

	ch, err := RisksStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	risks, errMsg, doneSeen := drainRisks(t, ch)

	if errMsg != "" {
		t.Fatalf("意外 error event: %s", errMsg)
	}
	if !doneSeen {
		t.Error("缺 done event")
	}
	if len(risks) != 2 {
		t.Fatalf("risks 数量 = %d，期望 2", len(risks))
	}
	if risks[0].File != "main.go" || risks[0].Severity != "high" || risks[0].Line != 42 {
		t.Errorf("risks[0] 字段错: %+v", risks[0])
	}
	if risks[0].Confidence < 0.94 || risks[0].Confidence > 0.96 {
		t.Errorf("risks[0].Confidence = %v，期望 ~0.95", risks[0].Confidence)
	}
	if risks[1].File != "util.go" || risks[1].Line != 0 {
		t.Errorf("risks[1] 字段错: %+v", risks[1])
	}
}

func TestRisksStage_Run_EmptyRisks(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = `{"risks":[]}`

	ch, err := RisksStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	risks, errMsg, doneSeen := drainRisks(t, ch)

	if errMsg != "" {
		t.Fatalf("意外 error: %s", errMsg)
	}
	if len(risks) != 0 {
		t.Errorf("risks 数量 = %d，期望 0", len(risks))
	}
	if !doneSeen {
		t.Error("缺 done event")
	}
}

func TestRisksStage_Run_MalformedJSON(t *testing.T) {
	p := llm.NewMockProvider()
	p.Reply = "not json at all"

	ch, err := RisksStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, errMsg, doneSeen := drainRisks(t, ch)

	if errMsg == "" {
		t.Fatal("应触发 error event")
	}
	if !strings.Contains(errMsg, "parse risks JSON") {
		t.Errorf("error message 应含 parse risks JSON，实际: %s", errMsg)
	}
	if doneSeen {
		t.Error("解析失败不应再 emit done")
	}
}

func TestRisksStage_Run_StreamError(t *testing.T) {
	_, err := RisksStage{}.Run(context.Background(), prctx.Context{L1Meta: "test"}, errProvider{err: streamErr{msg: "stream failed"}})
	if err == nil {
		t.Fatal("期望同步 Stream 错误向上冒")
	}
	if !strings.Contains(err.Error(), "stream") {
		t.Errorf("错误信息应含 stream，得到 %v", err)
	}
}
