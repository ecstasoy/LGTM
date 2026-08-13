// Package i18n resolves the review output locale. It is the Go counterpart of frontend/lib/i18n/locale.ts.
// Naming note: "locale" means UI / review language here. The unrelated notion of a PR's programming
// language lives in internal/api/lang.go.
package i18n

import (
	"sort"
	"strconv"
	"strings"
)

type Locale string

const (
	ZH Locale = "zh"
	EN Locale = "en"
)

// Normalize maps arbitrary input onto a supported locale, falling back to def rather than erroring.
func Normalize(v string, def Locale) Locale {
	switch Locale(strings.ToLower(strings.TrimSpace(v))) {
	case ZH:
		return ZH
	case EN:
		return EN
	default:
		return def
	}
}

// FromAcceptLanguage returns the highest-q supported tag in the header, or def.
func FromAcceptLanguage(header string, def Locale) Locale {
	if strings.TrimSpace(header) == "" {
		return def
	}

	type weighted struct {
		tag    string
		weight float64
	}
	var tags []weighted
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		tag := strings.ToLower(strings.TrimSpace(fields[0]))
		if tag == "" {
			continue
		}
		weight := 1.0
		for _, p := range fields[1:] {
			p = strings.TrimSpace(p)
			if !strings.HasPrefix(p, "q=") {
				continue
			}
			// A malformed q is treated as lowest priority rather than an error.
			if q, err := strconv.ParseFloat(strings.TrimPrefix(p, "q="), 64); err == nil {
				weight = q
			} else {
				weight = 0
			}
		}
		tags = append(tags, weighted{tag: tag, weight: weight})
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].weight > tags[j].weight })

	for _, t := range tags {
		switch {
		case t.tag == "zh" || strings.HasPrefix(t.tag, "zh-"):
			return ZH
		case t.tag == "en" || strings.HasPrefix(t.tag, "en-"):
			return EN
		}
	}
	return def
}
