package codex

import "testing"

// Usable must look at the displayable fields, not container presence: an
// info:{} with every child null renders nothing and is as unusable as
// info:null (PR #207 review should).
func TestTokenCountUsable(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"refused run (info null, no windows)",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":null,"secondary":null,"credits":{"has_credits":false},"plan_type":null}}}`,
			false},
		{"empty info object",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":{},"rate_limits":{"primary":null,"secondary":null}}}`,
			false},
		{"usage present",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"total_tokens":1}}}}`,
			true},
		{"window present",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":{"used_percent":5,"window_minutes":300}}}}`,
			true},
		{"plan present",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"plan_type":"prolite"}}}`,
			true},
		{"numeric credits present",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"credits":23.5}}}`,
			true},
	}
	for _, c := range cases {
		ev, ok := ParseEvent([]byte(c.line))
		if !ok {
			t.Fatalf("%s: ParseEvent failed", c.name)
		}
		tc := ev.TokenCount()
		if tc == nil {
			t.Fatalf("%s: TokenCount() = nil", c.name)
		}
		if got := tc.Usable(); got != c.want {
			t.Errorf("%s: Usable() = %v, want %v", c.name, got, c.want)
		}
	}
}
