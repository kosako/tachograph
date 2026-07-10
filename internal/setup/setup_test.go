package setup

import (
	"encoding/json"
	"os/exec"
	"runtime"
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
		{"double quote escaped", false, `/Users/x/my"dir/tacho`, `"/Users/x/my\"dir/tacho" statusline`},
		{"dollar escaped", false, "/Users/x/$HOME-ish/tacho", `"/Users/x/\$HOME-ish/tacho" statusline`},
		{"backtick escaped", false, "/Users/x/back`tick/tacho", "\"/Users/x/back\\`tick/tacho\" statusline"},
		{"windows backslashes untouched", false, `C:\Users\x\tacho.exe`, `C:\Users\x\tacho.exe statusline`},
		{"windows path with spaces keeps separators", false, `C:\Program Files\tacho.exe`, `"C:\Program Files\tacho.exe" statusline`},
		{"semicolon quoted", false, "/Users/x/semi;colon/tacho", `"/Users/x/semi;colon/tacho" statusline`},
		{"backslash before dollar escaped", false, `/Users/x/back\$lash/tacho`, `"/Users/x/back\\\$lash/tacho" statusline`},
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


// The quoted command must survive an actual POSIX shell: the first argv the
// shell reconstructs is exactly the original path, whatever it contains
// (#194 L-01). This is the property the escaping exists for.
func TestCommandShellRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exercises a POSIX shell")
	}
	paths := []string{
		"/Users/My Name/go/bin/tacho",
		`/Users/x/my"dir/tacho`,
		"/Users/x/$HOME-ish/tacho",
		"/Users/x/back`tick/tacho",
		"/Users/x/semi;colon and|pipe&amp/tacho",
		`/Users/x/back\$lash/tacho`,
		`/Users/x/double\\slash$x/tacho`,
		"/Users/x/paren(sub)>redir/tacho",
		"/Users/x/tilde~and*glob?/tacho",
	}
	for _, p := range paths {
		cmd := Command(false, p)
		quoted := strings.TrimSuffix(cmd, " statusline")
		out, err := exec.Command("sh", "-c", "printf %s "+quoted).Output()
		if err != nil {
			t.Fatalf("%q: sh failed: %v", p, err)
		}
		if string(out) != p {
			t.Errorf("shell round trip: %q became %q (command %q)", p, string(out), cmd)
		}
	}
}
