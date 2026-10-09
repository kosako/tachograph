package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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
		{"windows path quoted, separators unescaped", false, `C:\Users\x\tacho.exe`, `"C:\Users\x\tacho.exe" statusline`},
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

// A file holding just `null` decodes into a nil map rather than failing, so
// it needs its own check: without one, setting the key panics (#319).
func TestMergeSettingsRejectsNull(t *testing.T) {
	for _, in := range []string{"null", " null\n"} {
		if _, err := MergeSettings([]byte(in), "tacho statusline"); err == nil {
			t.Errorf("MergeSettings(%q) error = nil, want a non-object error", in)
		}
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
		`/tmp/back\slash/tacho`,
		`C:\Users\x\tacho.exe`,
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

// The generated plugin runs this binary by absolute path (SwiftBar's
// launchd PATH misses most install locations, #270), quoted when needed, and
// keeps the bundled plugin's metadata and display-tweak lines.
func TestSwiftBarPlugin(t *testing.T) {
	got := SwiftBarPlugin("/Users/me/go/bin/tacho")
	if !strings.HasSuffix(got, "\nexec /Users/me/go/bin/tacho swiftbar\n") {
		t.Errorf("exec line missing:\n%s", got)
	}
	if q := SwiftBarPlugin("/Users/me/My Tools/tacho"); !strings.HasSuffix(q, "\nexec \"/Users/me/My Tools/tacho\" swiftbar\n") {
		t.Errorf("path with a space not quoted:\n%s", q)
	}

	// Apart from the PATH note and the exec line, the plugin is the bundled
	// one line for line (metadata, PATH, display tweaks).
	contrib, err := os.ReadFile(filepath.Join("..", "..", "contrib", "tacho.30s.sh"))
	if err != nil {
		t.Fatal(err)
	}
	strip := func(script, note string) string {
		var keep []string
		inNote := false
		for _, line := range strings.Split(script, "\n") {
			switch {
			case strings.HasPrefix(line, note):
				inNote = true
				continue
			case inNote && strings.HasPrefix(line, "# "):
				continue
			case strings.HasPrefix(line, "exec "):
				line = "exec <tacho> swiftbar"
			}
			inNote = false
			keep = append(keep, line)
		}
		return strings.Join(keep, "\n")
	}
	// A Windows checkout may turn the file's line endings into CRLF. Elsewhere
	// a CR stays a failure: the script runs as is, and a CR in the shebang
	// breaks it.
	script := string(contrib)
	if runtime.GOOS == "windows" {
		script = strings.ReplaceAll(script, "\r\n", "\n")
	}
	want := strip(script, "# SwiftBar may not see your shell's PATH")
	if rest := strip(got, "# Written by `tacho setup swiftbar`"); rest != want {
		t.Errorf("plugin differs from contrib/tacho.30s.sh beyond the PATH note and exec line:\n%s\n--- want ---\n%s", rest, want)
	}
}
