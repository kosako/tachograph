package main

import (
	"testing"

	"github.com/kosako/tachograph/internal/testhome"
)

// TestMain gives the whole run a throwaway home (#377): see testhome.Run.
// SwiftBar's PluginDirectory setting is read with `defaults`, not from the
// home, so it is stubbed out here too: a test that wrongly went on to
// `setup swiftbar --write` would otherwise rewrite the machine's real plugin
// (#472). Tests that need a setting give one with isolateSwiftBar.
func TestMain(m *testing.M) {
	swiftBarPluginDirectory = func() string { return "" }
	testhome.Run(m)
}

// No test reads the machine's SwiftBar setting unless it stubs one in (#472).
func TestSwiftBarPluginDirectoryStubbed(t *testing.T) {
	if dir := swiftBarPluginDirectory(); dir != "" {
		t.Fatalf("swiftBarPluginDirectory() = %q, want the TestMain stub's \"\"", dir)
	}
}
