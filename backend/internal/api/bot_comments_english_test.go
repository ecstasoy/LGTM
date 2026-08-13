package api

import (
	"testing"
	"unicode"

	"github.com/ecstasoy/LGTM/backend/internal/review"
)

// containsCJK reports whether s has any CJK Unified Ideographs / Hiragana / Katakana / Hangul rune.
// Used to guard the "everything posted to GitHub is hardcoded English" rule (see comment.go's
// buildSuggestionCommentBody doc comment): these builders must never regress back to Chinese,
// regardless of DEFAULT_LOCALE.
func containsCJK(s string) bool {
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r),
			unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r),
			unicode.Is(unicode.Hangul, r):
			return true
		}
	}
	return false
}

// TestBuildBotReviewBody_NoCJK covers every trigger type buildBotReviewBody switches on
// (default/opened, synchronize, reopened, slash_review) — the scaffolding (headers, suggestion-count
// line, footer) must be plain English even though the summary argument itself is caller-supplied
// and may legitimately be non-English.
func TestBuildBotReviewBody_NoCJK(t *testing.T) {
	for _, trig := range []string{"opened", "synchronize", "reopened", "slash_review", ""} {
		got := buildBotReviewBody("English summary text.", "rev123", 2, trig)
		if containsCJK(got) {
			t.Errorf("trigger=%q: buildBotReviewBody output contains CJK:\n%s", trig, got)
		}
		// sanity: the builder actually produced the scaffolding, not an empty string
		if got == "" {
			t.Errorf("trigger=%q: buildBotReviewBody returned empty output", trig)
		}
	}
}

// TestBuildSuggestionCommentBody_NoCJK covers both the with-patch and without-patch shapes;
// this builder is shared by webhook.go's bot review and comment.go/commit.go's web UI adopt endpoints.
func TestBuildSuggestionCommentBody_NoCJK(t *testing.T) {
	withPatch := buildSuggestionCommentBody(review.Suggestion{
		Type:  "bug",
		Title: "Nil check missing",
		Body:  "This may panic on a nil pointer.",
		Patch: &review.Patch{After: "if x != nil {\n\t_ = x\n}"},
	})
	if containsCJK(withPatch) {
		t.Errorf("buildSuggestionCommentBody (with patch) contains CJK:\n%s", withPatch)
	}

	withoutPatch := buildSuggestionCommentBody(review.Suggestion{
		Type:  "style",
		Title: "Prefer early return",
		Body:  "Flatten this nested if.",
	})
	if containsCJK(withoutPatch) {
		t.Errorf("buildSuggestionCommentBody (without patch) contains CJK:\n%s", withoutPatch)
	}
}
