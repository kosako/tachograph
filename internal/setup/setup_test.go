package setup

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommand(t *testing.T) {
	cases := []struct {
		name       string
		bareIsSelf bool
		exe        string
		want       string
	}{
		{"bare tacho is this binary", true, "/Users/x/go/bin/tacho", "tacho statusline"},
		{"different or no PATH tacho: absolute", false, "/Users/x/go/bin/tacho", "/Users/x/go/bin/tacho statusline"},
		{"absolute path with spaces", false, "/Users/My Name/go/bin/tacho", `"/Users/My Name/go/bin/tacho" statusline`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Command(c.bareIsSelf, c.exe); got != c.want {
				t.Errorf("Command(%v, %q) = %q, want %q", c.bareIsSelf, c.exe, got, c.want)
			}
		})
	}
}

func TestMergeSettingsFresh(t *testing.T) {
	out, err := MergeSettings(nil, "tacho statusline")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	sl, ok := got["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine missing: %s", out)
	}
	if sl["command"] != "tacho statusline" || sl["type"] != "command" {
		t.Errorf("unexpected statusLine: %v", sl)
	}
}

func TestMergeSettingsPreservesOtherKeys(t *testing.T) {
	existing := []byte(`{"theme":"dark","statusLine":{"type":"command","command":"old","padding":1}}`)
	out, err := MergeSettings(existing, "/abs/tacho statusline")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["theme"] != "dark" {
		t.Errorf("theme not preserved: %v", got)
	}
	sl := got["statusLine"].(map[string]any)
	if sl["command"] != "/abs/tacho statusline" {
		t.Errorf("command not updated: %v", sl)
	}
}

func TestMergeSettingsRejectsNonObject(t *testing.T) {
	if _, err := MergeSettings([]byte(`["not","an","object"]`), "tacho statusline"); err == nil {
		t.Error("expected error for non-object settings")
	}
}

func TestSnippet(t *testing.T) {
	s := Snippet("tacho statusline")
	if !strings.Contains(s, `"statusLine"`) || !strings.Contains(s, `"tacho statusline"`) {
		t.Errorf("snippet missing pieces: %s", s)
	}
}
