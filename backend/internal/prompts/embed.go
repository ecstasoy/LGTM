// Package prompts embeds the *.tmpl prompt templates into the binary and hands them out by name.
package prompts

import (
	"embed"
	"fmt"
	"io/fs"
	"text/template"

	"github.com/ecstasoy/LGTM/backend/internal/i18n"
)

//go:embed *.tmpl
var files embed.FS

// Parse reads and compiles the named template.
func Parse(name string) (*template.Template, error) {
	src, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, fmt.Errorf("prompt %q: %w", name, err)
	}
	return template.New(name).Parse(string(src))
}

// ParseFor compiles the template for one stage in one locale, e.g. ("summary", EN) -> summary.en.tmpl.
// Callers must normalize the locale first; unknown values simply miss the embed FS and error out.
func ParseFor(stage string, locale i18n.Locale) (*template.Template, error) {
	return Parse(fmt.Sprintf("%s.%s.tmpl", stage, locale))
}
