package cache

import (
	"os"
	"path/filepath"
	"testing"
)

// A write whose rename fails leaves no temp file behind, on every system
// (#371): here a directory takes the cache file's place, which no rename can
// replace.
func TestWriteJSONRemovesTempWhenRenameFails(t *testing.T) {
	dir := setCacheDir(t)
	if err := os.Mkdir(filepath.Join(dir, "blocked.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON("blocked.json", map[string]int{"a": 1}); err == nil {
		t.Fatal("WriteJSON over a directory succeeded")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(leftovers) != 0 {
		t.Errorf("leftover temp files after a failed write: %v", leftovers)
	}
}
