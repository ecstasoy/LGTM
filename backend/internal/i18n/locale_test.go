package i18n_test

import (
	"testing"

	"github.com/ecstasoy/LGTM/backend/internal/i18n"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		in   string
		def  i18n.Locale
		want i18n.Locale
	}{
		{"zh", i18n.ZH, i18n.ZH},
		{"en", i18n.ZH, i18n.EN},
		{"EN", i18n.ZH, i18n.EN},
		{"fr", i18n.ZH, i18n.ZH},
		{"", i18n.ZH, i18n.ZH},
		{"", i18n.EN, i18n.EN},
		{"  zh  ", i18n.EN, i18n.ZH},              // TrimSpace test
		{"  EN  ", i18n.ZH, i18n.EN},              // TrimSpace + case test
	}
	for _, c := range cases {
		if got := i18n.Normalize(c.in, c.def); got != c.want {
			t.Errorf("Normalize(%q, %q) = %q, want %q", c.in, c.def, got, c.want)
		}
	}
}

func TestFromAcceptLanguage(t *testing.T) {
	cases := []struct {
		header string
		want   i18n.Locale
	}{
		{"zh-CN,zh;q=0.9,en;q=0.8", i18n.ZH},
		{"en-US,en;q=0.9", i18n.EN},
		{"en;q=0.8,zh-CN;q=0.9", i18n.ZH}, // q weight wins over source order
		{"EN-GB", i18n.EN},
		{"fr-FR,de;q=0.9", i18n.ZH},
		{"", i18n.ZH},
		{"en;q=abc,zh", i18n.ZH},
		{"en;q=0.9;q=0.1,zh;q=0.5", i18n.EN}, // first q= wins (0.9 > 0.5)
		{"*", i18n.ZH},                         // wildcard returns default
		{"en,zh", i18n.EN},                     // equal weight (1.0), source order preserved
	}
	for _, c := range cases {
		if got := i18n.FromAcceptLanguage(c.header, i18n.ZH); got != c.want {
			t.Errorf("FromAcceptLanguage(%q) = %q, want %q", c.header, got, c.want)
		}
	}
}
