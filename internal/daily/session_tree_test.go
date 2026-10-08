package daily

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// ClaudeSessionTree counts one session across its main transcript and the
// subagent / workflow transcripts nested under the same-named directory, in
// one pass with one dedup set: the cumulative tokens (session.tokens, #262)
// span every day and every file of the tree, today's portion only today's
// lines. A response repeated across files counts once.
func TestClaudeSessionTree(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	shared := claudeMsgID(now, "msg_shared", "req_shared", 10, 20, 100, 5) // 135, main and a subagent

	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath,
		claudeMsg(yesterday, 10, 10, 10, 10)+"\n"+shared+"\n", now) // 40 yesterday + 135 today
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "agent-a.jsonl"),
		shared+"\n"+claudeMsg(now, 1, 2, 50, 3)+"\n", now) // dup 135 + 56 today
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_1", "agent-b.jsonl"),
		claudeMsg(now, 4, 5, 60, 6)+"\n", now) // 75 today
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "workflows", "wf_1", "journal.jsonl"),
		`{"timestamp":"`+now.Format(time.RFC3339)+`","event":"started"}`+"\n", now)
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "old-agent.jsonl"),
		claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n", yesterday) // 4000, yesterday's file

	cum, today, ok := ClaudeSessionTree(mainPath, now, noPrices, nil)
	if !ok {
		t.Fatal("ok = false, want true (the main transcript has usage)")
	}
	if want := int64(40 + 135 + 56 + 75 + 4000); cum.Total != want {
		t.Errorf("cumulative Total = %d, want %d (every day, every file, the shared response once)", cum.Total, want)
	}
	if cum.Input+cum.Output != cum.Total {
		t.Errorf("cumulative Input+Output = %d, want Total %d", cum.Input+cum.Output, cum.Total)
	}
	if want := int64(135 + 56 + 75); today.Tokens != want {
		t.Errorf("today Tokens = %d, want %d (today's lines only)", today.Tokens, want)
	}
	// The wrapper session_today uses agrees with the tree's today.
	if got := ClaudeSessionToday(mainPath, now, noPrices); got.Tokens != today.Tokens {
		t.Errorf("ClaudeSessionToday = %d, want the tree's today %d", got.Tokens, today.Tokens)
	}
}

// The cumulative figure is unknown (ok=false) when the main transcript can't
// be read or carries no usage — never a partial zero. Today's portion keeps
// session_today's contract: whatever the readable files hold.
func TestClaudeSessionTreeUnknownMain(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "agent-a.jsonl"),
		claudeMsg(now, 1, 2, 50, 3)+"\n", now)

	if _, today, ok := ClaudeSessionTree(mainPath, now, noPrices, nil); ok || today.Tokens != 56 {
		t.Errorf("missing main: ok = %v, today = %d, want ok=false and today 56 from the subagent", ok, today.Tokens)
	}
	if err := os.MkdirAll(filepath.Dir(mainPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, mainPath, `{"type":"user","timestamp":"`+now.Format(time.RFC3339)+`"}`+"\n", now)
	if _, _, ok := ClaudeSessionTree(mainPath, now, noPrices, nil); ok {
		t.Error("main without usage: ok = true, want false")
	}
	if _, _, ok := ClaudeSessionTree("", now, noPrices, nil); ok {
		t.Error("empty path: ok = true, want false")
	}
}

type memTreeCache struct {
	m    map[string]memTreeEntry
	puts int
}

type memTreeEntry struct {
	size  int64
	mtime time.Time
	data  []byte
}

func (c *memTreeCache) Get(path string, size int64, mtime time.Time) ([]byte, bool) {
	e, ok := c.m[path]
	if !ok || e.size != size || !e.mtime.Equal(mtime) {
		return nil, false
	}
	return e.data, true
}

func (c *memTreeCache) Put(path string, size int64, mtime time.Time, data []byte) {
	c.m[path] = memTreeEntry{size, mtime, data}
	c.puts++
}

// A nested transcript last written before today is read once and then served
// from the cache while its size and mtime are unchanged — the statusline no
// longer re-reads a long session's finished subagents on every call (#262).
// Today's files are always read (today's portion needs their lines).
func TestClaudeSessionTreeCachesOlderFiles(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath, claudeMsg(now, 10, 20, 100, 5)+"\n", now) // 135
	old := filepath.Join(root, "projects", "p", "main", "subagents", "old-agent.jsonl")
	writeFile(t, old, claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n", yesterday) // 4000
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "new-agent.jsonl"),
		claudeMsg(now, 1, 2, 50, 3)+"\n", now) // 56

	fc := &memTreeCache{m: map[string]memTreeEntry{}}
	cum, today, _ := ClaudeSessionTree(mainPath, now, noPrices, fc)
	if cum.Total != 135+4000+56 || today.Tokens != 135+56 {
		t.Fatalf("first pass: cumulative %d / today %d, want %d / %d", cum.Total, today.Tokens, 135+4000+56, 135+56)
	}
	if fc.puts != 1 {
		t.Fatalf("puts = %d, want 1 (only yesterday's file is cached)", fc.puts)
	}

	// Rewrite the old file with same-size content and restore its mtime: an
	// unchanged size+mtime must be served from the cache, not re-read.
	writeFile(t, old, claudeMsg(yesterday, 2000, 1000, 1000, 1000)+"\n", yesterday) // same size, 5000 if re-read
	if cum, _, _ := ClaudeSessionTree(mainPath, now, noPrices, fc); cum.Total != 135+4000+56 {
		t.Errorf("second pass: cumulative %d, want %d from the cache", cum.Total, 135+4000+56)
	}
	if fc.puts != 1 {
		t.Errorf("puts = %d after an unchanged second pass, want 1", fc.puts)
	}

	// A changed size invalidates the entry.
	writeFile(t, old, claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n"+claudeMsg(yesterday, 1, 1, 1, 1)+"\n", yesterday)
	if cum, _, _ := ClaudeSessionTree(mainPath, now, noPrices, fc); cum.Total != 135+4000+4+56 {
		t.Errorf("after growth: cumulative %d, want %d (re-read)", cum.Total, 135+4000+4+56)
	}
}

// Codex review (#262): an older nested file must be deduplicated against the
// rest of the tree too. A response present in both the main transcript and a
// child counts once — on the day it happened and on every later day, when the
// child has become an older (cached) file.
func TestClaudeSessionTreeDedupsOlderFilesAcrossTree(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	shared := claudeMsgID(yesterday, "msg_shared", "req_shared", 10, 20, 100, 5) // 135
	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath, shared+"\n"+claudeMsg(now, 1, 2, 50, 3)+"\n", now) // 135 + 56
	writeFile(t, filepath.Join(root, "projects", "p", "main", "subagents", "child.jsonl"),
		shared+"\n"+claudeMsg(yesterday, 4, 5, 60, 6)+"\n", yesterday) // dup 135 + 75, an older file

	fc := &memTreeCache{m: map[string]memTreeEntry{}}
	for pass, cache := range []TreeFileCache{nil, fc, fc} { // uncached, filling the cache, from the cache
		if cum, _, _ := ClaudeSessionTree(mainPath, now, noPrices, cache); cum.Total != 135+56+75 {
			t.Errorf("pass %d: cumulative %d, want %d (the shared response once)", pass, cum.Total, 135+56+75)
		}
	}
}

// withFormat re-tags an encoded fileUsage as another cache format.
func withFormat(b []byte, format uint64) []byte {
	_, n := binary.Uvarint(b)
	return append(binary.AppendUvarint(nil, format), b[n:]...)
}

// decodeFileUsage reads whatever the session-tree cache hands back, which
// need not be what this version wrote: anything malformed or of another
// format must read as a miss, never as usage (#355).
func TestDecodeFileUsage(t *testing.T) {
	u := fileUsage{
		unkeyed: usageRecord{input: 1, cached: 2, outp: 3},
		keyed: []usageRecord{
			{key: 7, input: 10, cached: 20, outp: 30},
			{key: 1 << 40, input: 300, outp: 1 << 33},
		},
	}
	valid := u.encode()
	got, ok := decodeFileUsage(valid)
	if !ok || got.unkeyed != u.unkeyed || len(got.keyed) != len(u.keyed) {
		t.Fatalf("round trip = %+v (ok %v), want %+v", got, ok, u)
	}
	for i := range u.keyed {
		if got.keyed[i] != u.keyed[i] {
			t.Errorf("round trip keyed[%d] = %+v, want %+v", i, got.keyed[i], u.keyed[i])
		}
	}

	// A record count no blob could hold is rejected before it sizes an
	// allocation.
	hugeCount := binary.AppendUvarint(nil, fileUsageFormat)
	for range 3 {
		hugeCount = binary.AppendUvarint(hugeCount, 1)
	}
	hugeCount = binary.AppendUvarint(hugeCount, 1<<62)
	malformed := map[string][]byte{
		"empty":                     nil,
		"another format":            withFormat(valid, fileUsageFormat+1),
		"bytes left over":           append(append([]byte(nil), valid...), 0),
		"record count past the end": hugeCount,
		"varint overflow":           append(binary.AppendUvarint(nil, fileUsageFormat), append(bytes.Repeat([]byte{0x80}, 10), 1)...),
	}
	// Cut short anywhere — inside any field, or between them.
	for i := 1; i < len(valid); i++ {
		malformed[fmt.Sprintf("cut to %d of %d bytes", i, len(valid))] = valid[:i]
	}
	for name, b := range malformed {
		if got, ok := decodeFileUsage(b); ok {
			t.Errorf("%s: decoded as %+v, want a miss", name, got)
		}
	}
}

// A cached blob that doesn't decode is neither counted nor kept: the file is
// read again from its transcript and the entry replaced, so the next call is
// served from a good blob (#355). Each blob holds other usage than the file,
// so a decoder that let it through would show in the total.
func TestClaudeSessionTreeRereadsUndecodableCache(t *testing.T) {
	now := time.Now()
	yesterday := DayStart(now).Add(-time.Hour)
	other := fileUsage{unkeyed: usageRecord{input: 9999}}.encode()
	for name, blob := range map[string][]byte{
		"cut short":       other[:len(other)-1],
		"another format":  withFormat(other, fileUsageFormat+1),
		"bytes left over": append(append([]byte(nil), other...), 0),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
			writeFile(t, mainPath, claudeMsg(now, 10, 20, 100, 5)+"\n", now) // 135
			old := filepath.Join(root, "projects", "p", "main", "subagents", "old-agent.jsonl")
			writeFile(t, old, claudeMsg(yesterday, 1000, 1000, 1000, 1000)+"\n", yesterday) // 4000
			info, err := os.Stat(old)
			if err != nil {
				t.Fatal(err)
			}
			fc := &memTreeCache{m: map[string]memTreeEntry{old: {info.Size(), info.ModTime(), blob}}}

			if cum, _, _ := ClaudeSessionTree(mainPath, now, noPrices, fc); cum.Total != 135+4000 {
				t.Fatalf("cumulative %d, want %d (the old file read again)", cum.Total, 135+4000)
			}
			if fc.puts != 1 {
				t.Fatalf("puts = %d, want 1 (the entry replaced)", fc.puts)
			}
			if _, ok := decodeFileUsage(fc.m[old].data); !ok {
				t.Fatal("the replaced entry does not decode")
			}
			if cum, _, _ := ClaudeSessionTree(mainPath, now, noPrices, fc); cum.Total != 135+4000 || fc.puts != 1 {
				t.Errorf("second pass: cumulative %d / puts %d, want %d / 1 (served from the replaced entry)", cum.Total, fc.puts, 135+4000)
			}
		})
	}
}

// Codex review (#262): today's portion keeps session_today's own dedup — a
// response whose content blocks straddle midnight still counts today, as it
// always did (the window is checked before dedup, so yesterday's block can't
// hide today's).
func TestClaudeSessionTreeTodayAcrossMidnight(t *testing.T) {
	root := t.TempDir()
	start := DayStart(time.Now())
	now := start.Add(time.Hour)
	before := claudeMsgID(start.Add(-time.Second), "msg_mid", "req_mid", 10, 20, 100, 5)
	after := claudeMsgID(start.Add(time.Second), "msg_mid", "req_mid", 10, 20, 100, 5)
	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath, before+"\n"+after+"\n", now)

	cum, today, _ := ClaudeSessionTree(mainPath, now, noPrices, nil)
	if today.Tokens != 135 {
		t.Errorf("today = %d, want 135 (the block after midnight counts today)", today.Tokens)
	}
	if cum.Total != 135 {
		t.Errorf("cumulative = %d, want 135 (one response)", cum.Total)
	}
}

// Codex review (#262): a nested transcript that can't be read makes the
// cumulative figure unknown rather than a partial sum that would overwrite a
// complete one; today's portion keeps counting what is readable.
func TestClaudeSessionTreeUnreadableChildIsUnknown(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions enforced for the current user")
	}
	root := t.TempDir()
	now := time.Now()
	mainPath := filepath.Join(root, "projects", "p", "main.jsonl")
	writeFile(t, mainPath, claudeMsg(now, 10, 20, 100, 5)+"\n", now) // 135
	blocked := filepath.Join(root, "projects", "p", "main", "subagents", "blocked.jsonl")
	writeFile(t, blocked, claudeMsg(now, 1, 2, 50, 3)+"\n", now)
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o644) })

	_, today, ok := ClaudeSessionTree(mainPath, now, noPrices, nil)
	if ok {
		t.Error("ok = true with an unreadable nested transcript, want false (cumulative unknown)")
	}
	if today.Tokens != 135 {
		t.Errorf("today = %d, want 135 from the readable main transcript", today.Tokens)
	}
}
