package claude

import (
	"strings"
	"testing"
)

// lastUsageLine walks a transcript from the end and finds the line
// EachUsageLine visits last, whatever follows it: turns without usage, blank
// and CRLF line ends, lines that only mention "usage", malformed or
// half-written lines (#375).
func TestLastUsageLine(t *testing.T) {
	response := func(ts string) string {
		return `{"timestamp":"` + ts + `","message":{"id":"msg_` + ts + `","usage":{"input_tokens":1,"output_tokens":1}}}`
	}
	block := func(ts string) string { // one content block of response msg_a
		return `{"timestamp":"` + ts + `","requestId":"req_a","message":{"id":"msg_a","usage":{"input_tokens":1,"output_tokens":1}}}`
	}
	cases := []struct {
		name string
		in   string
		want string // the found line's timestamp; "" when there is none
	}{
		{name: "empty", in: "", want: ""},
		{name: "no usage", in: `{"type":"user","timestamp":"t1"}` + "\n", want: ""},
		{name: "one line, no newline", in: response("t1"), want: "t1"},
		{name: "the first line", in: response("t1") + "\n" + `{"type":"user","timestamp":"t2"}`, want: "t1"},
		{name: "the last of several", in: response("t1") + "\n" + response("t2") + "\n" + response("t3") + "\n", want: "t3"},
		{name: "blank and CRLF line ends", in: response("t1") + "\r\n" + response("t2") + "\r\n\r\n\n  \n", want: "t2"},
		{
			name: "later turns without usage",
			in: response("t1") + "\n" + response("t2") + "\n" + `{"type":"user","timestamp":"t3"}` + "\n" +
				`{"type":"user","timestamp":"t4","content":"the \"usage\" flag"}` + "\n",
			want: "t2",
		},
		{
			name: "usage outside an assistant message",
			in: response("t1") + "\n" + `{"timestamp":"t2","message":{"usage":null}}` + "\n" +
				`{"timestamp":"t3","usage":{"input_tokens":1}}` + "\n",
			want: "t1",
		},
		{
			name: "malformed and half-written lines",
			in: response("t1") + "\n" + `{"timestamp":"t2","message":{"usage":` + "\n" +
				strings.TrimSuffix(response("t3"), "}}}"),
			want: "t1",
		},
		{name: "the newest block of one response", in: block("t1") + "\n" + block("t2") + "\n" + block("t3") + "\n", want: "t3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := lastUsageLine([]byte(c.in))
			if ok != (c.want != "") || got.Timestamp != c.want {
				t.Errorf("lastUsageLine = %q, %v, want %q", got.Timestamp, ok, c.want)
			}
			last := ""
			EachUsageLine([]byte(c.in), func(l TranscriptLine) { last = l.Timestamp })
			if last != c.want {
				t.Errorf("EachUsageLine's last line = %q, want %q", last, c.want)
			}
		})
	}
}
