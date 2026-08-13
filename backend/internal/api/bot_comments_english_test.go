package api

import (
	"strings"
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

// TestBuildBotReviewBody_SuggestionCountWording pins the exact sentence the suggestion-count line
// renders for zero / one / many, so number agreement (and the zero-suggestion wording, which must
// not reference an inline comment or an Apply button that don't exist) cannot silently regress.
// This guards the defect class called out in review: a mechanical translation of the original
// Chinese (which has no singular/plural distinction) reintroduced "Generated **1** suggestions".
func TestBuildBotReviewBody_SuggestionCountWording(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{0, "✨ No inline suggestions this time — nothing stood out at the line level.\n\n"},
		{1, "✨ Generated **1** suggestion, attached as an inline comment. Use GitHub's built-in \"Apply suggestion\" to commit it with a click.\n\n"},
		{2, "✨ Generated **2** suggestions, attached as inline comments. Use GitHub's built-in \"Apply suggestion\" to commit any of them with a click.\n\n"},
	}
	for _, c := range cases {
		got := buildBotReviewBody("summary", "rev123", c.count, "opened")
		if !strings.Contains(got, c.want) {
			t.Errorf("count=%d: expected body to contain %q, got:\n%s", c.count, c.want, got)
		}
		// zero must not claim an inline comment or an Apply button exists
		if c.count == 0 {
			if strings.Contains(got, "inline comment") && !strings.Contains(got, "No inline suggestions") {
				t.Errorf("count=0: body should not reference inline comments outside the no-suggestions sentence:\n%s", got)
			}
			if strings.Contains(got, "Apply suggestion") {
				t.Errorf("count=0: body should not tell the reader to click Apply suggestion when there are none:\n%s", got)
			}
		}
		// singular/plural: "1 suggestions" must never appear, "2 suggestion," (missing s) must never appear
		if strings.Contains(got, "**1** suggestions") {
			t.Errorf("count=%d: found unagreed plural '1 suggestions':\n%s", c.count, got)
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
