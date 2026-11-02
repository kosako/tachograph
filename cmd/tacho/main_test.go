package main

import (
	"reflect"
	"testing"

	"github.com/kosako/tachograph/internal/testhome"
)

// TestMain gives the whole run a throwaway home (#377): see testhome.Run.
// SwiftBar's PluginDirectory setting is read with `defaults`, not from the
// home, so it is stubbed out here too: a test that wrongly went on to
// `setup swiftbar --write` would otherwise rewrite the machine's real plugin
// (#472). Tests that need a setting give one with isolateSwiftBar.
func TestMain(m *testing.M) {
	swiftBarPluginDirectory = noSwiftBarSetting
	testhome.Run(m)
}

// noSwiftBarSetting stands in for SwiftBar's PluginDirectory setting in tests.
func noSwiftBarSetting() string { return "" }

// No test reads the machine's SwiftBar setting unless it stubs one in (#472).
// The check compares the functions themselves, not the value returned, so it
// fails without the stub even on a machine where SwiftBar has no setting.
func TestSwiftBarPluginDirectoryStubbed(t *testing.T) {
	if reflect.ValueOf(swiftBarPluginDirectory).Pointer() != reflect.ValueOf(noSwiftBarSetting).Pointer() {
		t.Fatal("swiftBarPluginDirectory is not TestMain's noSwiftBarSetting stub")
	}
}
