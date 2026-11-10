package review

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/ecstasoy/LGTM/backend/internal/prctx"
)

// diffLine is one new-side (RIGHT) line of a unified diff: a context or added line.
type diffLine struct {
	n    int
	text string
}

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// rightSideHunks returns each hunk's new-side lines with their line numbers in the new file.
func rightSideHunks(patch string) [][]diffLine {
	var hunks [][]diffLine
	var cur []diffLine
	n, inHunk := 0, false
	for raw := range strings.SplitSeq(patch, "\n") {
		raw = strings.TrimSuffix(raw, "\r")
		if m := hunkHeader.FindStringSubmatch(raw); m != nil {
			if len(cur) > 0 {
				hunks = append(hunks, cur)
			}
			cur, inHunk = nil, true
			n, _ = strconv.Atoi(m[1])
			continue
		}
		if !inHunk || raw == "" {
			continue
		}
		switch raw[0] {
		case ' ', '+':
			cur = append(cur, diffLine{n: n, text: raw[1:]})
			n++
		}
	}
	if len(cur) > 0 {
		hunks = append(hunks, cur)
	}
	return hunks
}

// AnchorSuggestions pins each suggestion to new-side lines of its file's diff so GitHub can attach it.
// A patch whose before cannot be found verbatim is dropped; a line outside the diff is flagged OutsideDiff.
func AnchorSuggestions(sgs []Suggestion, files []prctx.FileContext) []Suggestion {
	byPath := make(map[string][][]diffLine, len(files))
	for _, f := range files {
		byPath[f.Path] = rightSideHunks(f.Patch)
	}
	out := make([]Suggestion, len(sgs))
	for i, s := range sgs {
		out[i] = anchorOne(s, byPath[s.File])
	}
	return out
}

func anchorOne(s Suggestion, hunks [][]diffLine) Suggestion {
	s.StartLine, s.OutsideDiff = 0, false
	if s.Patch != nil {
		if start, end, ok := locate(s.Patch.Before, s.Line, hunks); ok {
			s.Line = end
			if start < end {
				s.StartLine = start
			}
			return s
		}
		s.Patch = nil
	}
	s.OutsideDiff = !onRightSide(s.Line, hunks)
	return s
}

// locate finds before as consecutive new-side lines within one hunk, preferring the match nearest line.
func locate(before string, line int, hunks [][]diffLine) (start, end int, ok bool) {
	want := normalizedLines(before)
	if len(want) == 0 {
		return 0, 0, false
	}
	best := -1
	for _, h := range hunks {
		for i := 0; i+len(want) <= len(h); i++ {
			if !matchesAt(h[i:i+len(want)], want) {
				continue
			}
			s, e := h[i].n, h[i+len(want)-1].n
			if d := distance(line, s, e); best < 0 || d < best {
				best, start, end = d, s, e
			}
		}
	}
	return start, end, best >= 0
}

func matchesAt(got []diffLine, want []string) bool {
	for j, w := range want {
		if normalize(got[j].text) != w {
			return false
		}
	}
	return true
}

// normalizedLines splits a snippet into whitespace-normalized lines, trimming blank lines at both ends.
func normalizedLines(snippet string) []string {
	lines := strings.Split(strings.ReplaceAll(snippet, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = normalize(lines[i])
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func normalize(s string) string { return strings.Join(strings.Fields(s), " ") }

func distance(line, start, end int) int {
	switch {
	case line < start:
		return start - line
	case line > end:
		return line - end
	default:
		return 0
	}
}

func onRightSide(line int, hunks [][]diffLine) bool {
	for _, h := range hunks {
		if len(h) > 0 && line >= h[0].n && line <= h[len(h)-1].n {
			return true
		}
	}
	return false
}
