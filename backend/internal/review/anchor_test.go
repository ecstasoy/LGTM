package review

import (
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// anchorPatch new-side lines: hunk 1 is 10–17, hunk 2 is 42–44.
const anchorPatch = `@@ -10,6 +10,8 @@ func (s *Store) Put(k, v string) {
 	if k == "" {
 		return
 	}
-	s.items[k] = v
+	s.mu.Lock()
+	s.items[k] = v
+	s.mu.Unlock()
 	s.count++
 }
@@ -40,3 +42,4 @@ func (s *Store) Get(k string) string {
 	return s.items[k]
 }
+// trailing
\ No newline at end of file`

func anchorFiles() []prctx.FileContext {
	return []prctx.FileContext{{Path: "store.go", Patch: anchorPatch}}
}

func withPatch(line int, before, after string) Suggestion {
	return Suggestion{File: "store.go", Line: line, Type: "bug", Title: "t", Body: "b",
		Patch: &Patch{Lang: "go", Before: before, After: after}}
}

func TestRightSideHunks(t *testing.T) {
	hunks := rightSideHunks(anchorPatch)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2", len(hunks))
	}
	first, last := hunks[0][0], hunks[0][len(hunks[0])-1]
	if first.n != 10 || last.n != 17 || len(hunks[0]) != 8 {
		t.Errorf("hunk 1 = %d..%d (%d lines), want 10..17 (8 lines)", first.n, last.n, len(hunks[0]))
	}
	if hunks[0][4].text != "\ts.items[k] = v" || hunks[0][4].n != 14 {
		t.Errorf("line 14 = %+v, want the added s.items line", hunks[0][4])
	}
	if got := hunks[1][len(hunks[1])-1]; got.n != 44 || got.text != "// trailing" {
		t.Errorf("hunk 2 last = %+v, want 44 // trailing", got)
	}
}

func TestAnchorSuggestions(t *testing.T) {
	cases := []struct {
		name        string
		in          Suggestion
		wantStart   int
		wantLine    int
		wantPatch   bool
		wantOutside bool
	}{
		{
			name:      "multi-line before pins the whole range",
			in:        withPatch(20, "s.mu.Lock()\ns.items[k] = v\ns.mu.Unlock()", "s.put(k, v)"),
			wantStart: 13, wantLine: 15, wantPatch: true,
		},
		{
			name:     "single-line before corrects a drifted line",
			in:       withPatch(12, "s.items[k] = v", "s.items[k] = strings.TrimSpace(v)"),
			wantLine: 14, wantPatch: true,
		},
		{
			name:      "indentation and blank edges are ignored",
			in:        withPatch(11, "\n  if k == \"\" {\n      return\n  }\n\n", "if k == \"\" {\n\treturn\n}"),
			wantStart: 10, wantLine: 12, wantPatch: true,
		},
		{
			name:     "duplicate matches pick the one nearest the model's line",
			in:       withPatch(42, "}", "} // end"),
			wantLine: 43, wantPatch: true,
		},
		{
			name:     "before missing from the diff drops the patch but keeps an in-diff line",
			in:       withPatch(11, "s.items[k] = v + 1", "s.items[k] = v"),
			wantLine: 11,
		},
		{
			name:        "before missing and line outside the diff",
			in:          withPatch(30, "nope()", "yes()"),
			wantLine:    30,
			wantOutside: true,
		},
		{
			name:     "before spanning two hunks does not match",
			in:       withPatch(17, "}\nreturn s.items[k]", "x"),
			wantLine: 17,
		},
		{
			name:     "after without before cannot be anchored",
			in:       withPatch(14, "  ", "s.items[k] = v"),
			wantLine: 14,
		},
		{
			name:     "text-only suggestion on a diff line is untouched",
			in:       Suggestion{File: "store.go", Line: 16, Type: "style", Title: "t", Body: "b"},
			wantLine: 16,
		},
		{
			name:        "text-only suggestion between hunks is outside the diff",
			in:          Suggestion{File: "store.go", Line: 25, Type: "style", Title: "t", Body: "b"},
			wantLine:    25,
			wantOutside: true,
		},
		{
			name:        "file without a supplied diff is outside the diff",
			in:          Suggestion{File: "other.go", Line: 3, Type: "bug", Title: "t", Body: "b", Patch: &Patch{Before: "a", After: "b"}},
			wantLine:    3,
			wantOutside: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AnchorSuggestions([]Suggestion{tc.in}, anchorFiles())[0]
			if got.StartLine != tc.wantStart || got.Line != tc.wantLine {
				t.Errorf("range = %d..%d, want %d..%d", got.StartLine, got.Line, tc.wantStart, tc.wantLine)
			}
			if (got.Patch != nil) != tc.wantPatch {
				t.Errorf("patch kept = %v, want %v", got.Patch != nil, tc.wantPatch)
			}
			if got.OutsideDiff != tc.wantOutside {
				t.Errorf("OutsideDiff = %v, want %v", got.OutsideDiff, tc.wantOutside)
			}
		})
	}
}

func TestAnchorSuggestions_DoesNotMutateInput(t *testing.T) {
	in := []Suggestion{withPatch(30, "nope()", "yes()")}
	_ = AnchorSuggestions(in, anchorFiles())
	if in[0].Patch == nil {
		t.Error("input suggestion lost its patch; AnchorSuggestions must return copies")
	}
}
