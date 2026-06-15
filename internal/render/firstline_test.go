package render

import "testing"

func TestFirstTemplateLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"only comments", "# a\n# b\n", ""},
		{"plain line", "{claude.model}", "{claude.model}"},
		{"skips comments and blanks", "# header\n\n  # indented comment\n{claude.5h.pct}\n", "{claude.5h.pct}"},
		{"first of many", "{a}\n{b}", "{a}"},
		{"trims whitespace", "   {claude.model}   \n", "{claude.model}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FirstTemplateLine(c.in); got != c.want {
				t.Errorf("FirstTemplateLine(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
