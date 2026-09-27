package codex

import (
	"encoding/json"
	"testing"
)

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
		{"credits object with a balance",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":null,"secondary":null,"credits":{"has_credits":true,"unlimited":false,"balance":"23.5"},"plan_type":null}}}`,
			true},
		{"credits object without credits",
			`{"timestamp":"2026-07-10T13:34:51Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"primary":null,"secondary":null,"credits":{"has_credits":false,"unlimited":false,"balance":"0"},"plan_type":null}}}`,
			false},
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

// Current Codex writes rate_limits.credits as an object with a string
// balance; only a finite balance on an account that has (limited) credits is
// shown (#267).
func TestCreditsBalance(t *testing.T) {
	cases := []struct {
		name    string
		credits string
		want    float64
		wantOK  bool
	}{
		{"object with a balance", `{"has_credits":true,"unlimited":false,"balance":"23.5"}`, 23.5, true},
		{"object with a zero balance", `{"has_credits":true,"unlimited":false,"balance":"0"}`, 0, true},
		{"number (older Codex)", `23.5`, 23.5, true},
		{"no credits on the plan", `{"has_credits":false,"unlimited":false,"balance":"0"}`, 0, false},
		{"has_credits missing", `{"unlimited":false,"balance":"5"}`, 0, false},
		{"unlimited", `{"has_credits":true,"unlimited":true,"balance":"0"}`, 0, false},
		{"balance null", `{"has_credits":true,"unlimited":false,"balance":null}`, 0, false},
		{"balance not a number", `{"has_credits":true,"unlimited":false,"balance":"n/a"}`, 0, false},
		{"balance NaN", `{"has_credits":true,"unlimited":false,"balance":"NaN"}`, 0, false},
		{"balance Inf", `{"has_credits":true,"unlimited":false,"balance":"Inf"}`, 0, false},
		{"null", `null`, 0, false},
	}
	for _, c := range cases {
		var v any
		if err := json.Unmarshal([]byte(c.credits), &v); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, ok := creditsBalance(v)
		if ok != c.wantOK || got != c.want {
			t.Errorf("%s: creditsBalance = (%v, %v), want (%v, %v)", c.name, got, ok, c.want, c.wantOK)
		}
	}
}
