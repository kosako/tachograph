package pricing

import (
	"os"
	"path/filepath"
	"testing"
)

// Validate must reject exactly what Load ignores: Load drops the whole
// pricing.json on any decode error, including a wrongly typed value, so
// doctor relies on Validate to surface it (#260).
func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"valid partial override", `{"claude-opus":{"input":99}}`, true},
		{"empty object", `{}`, true},
		{"string price", `{"claude-fable":{"input":"10"}}`, false},
		{"rate not an object", `{"claude-fable":10}`, false},
		{"top level not an object", `[1,2]`, false},
		{"syntax error", `{bad`, false},
	}
	for _, c := range cases {
		if err := Validate([]byte(c.in)); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

// Whatever Validate rejects, Load ignores in full (built-in prices stand);
// whatever it accepts, Load applies. Pins the shared decode path.
func TestLoadAgreesWithValidate(t *testing.T) {
	for _, c := range []struct {
		in      string
		wantIn  float64
		applied bool
	}{
		{`{"claude-opus":{"input":99}}`, 99, true},
		{`{"claude-fable":{"input":"10"},"claude-opus":{"input":99}}`, 5, false},
	} {
		dir := t.TempDir()
		t.Setenv("TACHO_CONFIG_DIR", dir)
		if err := os.WriteFile(filepath.Join(dir, "pricing.json"), []byte(c.in), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := Validate([]byte(c.in)) == nil; got != c.applied {
			t.Errorf("%s: Validate ok = %v, want %v", c.in, got, c.applied)
		}
		r, _ := Load().For("claude-opus")
		if r.In != c.wantIn {
			t.Errorf("%s: claude-opus input = %v, want %v", c.in, r.In, c.wantIn)
		}
	}
}
