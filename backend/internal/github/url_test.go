package github

import "testing"

func TestParseURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		owner   string
		repo    string
		number  int
		wantErr bool
	}{
		{"基本形态", "https://github.com/ecstasoy/LGTM/pull/3", "ecstasoy", "LGTM", 3, false},
		{"带 /files 后缀", "https://github.com/golang/go/pull/12345/files", "golang", "go", 12345, false},
		{"末尾斜杠", "https://github.com/golang/go/pull/1/", "golang", "go", 1, false},
		{"前后空白", "  https://github.com/golang/go/pull/1  ", "golang", "go", 1, false},
		{"非 github 域名", "https://gitlab.com/owner/repo/pull/1", "", "", 0, true},
		{"非 pull 路径", "https://github.com/golang/go/issues/1", "", "", 0, true},
		{"编号缺失", "https://github.com/golang/go/pull/", "", "", 0, true},
		{"编号非数字", "https://github.com/golang/go/pull/abc", "", "", 0, true},
		{"编号为 0", "https://github.com/golang/go/pull/0", "", "", 0, true},
		{"编号为负", "https://github.com/golang/go/pull/-5", "", "", 0, true},
		{"空串", "", "", "", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			owner, repo, number, err := ParseURL(c.input)
			if c.wantErr {
				if err == nil {
					t.Fatalf("期望错误，但 owner=%q repo=%q number=%d", owner, repo, number)
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			if owner != c.owner || repo != c.repo || number != c.number {
				t.Errorf("got (%q, %q, %d), want (%q, %q, %d)",
					owner, repo, number, c.owner, c.repo, c.number)
			}
		})
	}
}
