package menubar

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"testing"

	"github.com/kosako/tachograph/internal/render"
	"github.com/kosako/tachograph/internal/schema"
)

func toolWith(name string, pct5 float64) schema.Tool {
	m5 := 300
	return schema.Tool{
		Tool:      name,
		Available: true,
		Backend:   schema.BackendSubscription,
		Limits:    []schema.Limit{{Window: "5h", WindowMinutes: &m5, UsedPct: &pct5}},
	}
}

func decode(t *testing.T, b64 string) (w, h int) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

func TestPNGBase64Dimensions(t *testing.T) {
	s := schema.Status{Tools: []schema.Tool{
		toolWith(schema.ToolClaudeCode, 34),
		toolWith(schema.ToolCodex, 78),
	}}
	b64, ok := PNGBase64(s, true, render.MetricLimit5h)
	if !ok {
		t.Fatal("PNGBase64 not ok")
	}
	w, h := decode(t, b64)
	if want := canvas*2 + gap; w != want {
		t.Errorf("width = %d, want %d", w, want)
	}
	if h != canvas {
		t.Errorf("height = %d, want %d", h, canvas)
	}
}

func TestPNGBase64Empty(t *testing.T) {
	if _, ok := PNGBase64(schema.Status{}, true, render.MetricLimit5h); ok {
		t.Error("PNGBase64 should be !ok with no tools")
	}
}

// The fill must track 5h headroom: the ring drains as the window is used, so
// a lightly used gauge has strictly more ink than a heavily used one (#223).
func TestFillScalesWithHeadroom(t *testing.T) {
	ink := func(pct float64) int {
		b64, ok := PNGBase64(schema.Status{Tools: []schema.Tool{toolWith(schema.ToolClaudeCode, pct)}}, true, render.MetricLimit5h)
		if !ok {
			t.Fatal("not ok")
		}
		raw, _ := base64.StdEncoding.DecodeString(b64)
		img, _ := png.Decode(bytes.NewReader(raw))
		var sum int
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				_, _, _, a := img.At(x, y).RGBA()
				sum += int(a >> 8)
			}
		}
		return sum
	}
	light, heavy := ink(10), ink(90)
	if light <= heavy {
		t.Errorf("expected more ink at 10%% used (%d) than 90%% used (%d)", light, heavy)
	}
}

// A tool reporting only a weekly window must still fill the ring when the
// menu bar metric is limit_5h — the ring falls back to the available window
// (Codex dropped its 5h window in 2026-07, issue #210).
func TestFallbackFillsRingFromAvailableWindow(t *testing.T) {
	ink := func(pctW float64) int {
		mW := 10080
		weeklyOnly := schema.Tool{
			Tool:      schema.ToolCodex,
			Available: true,
			Backend:   schema.BackendSubscription,
			Limits:    []schema.Limit{{Window: "weekly", WindowMinutes: &mW, UsedPct: &pctW}},
		}
		b64, ok := PNGBase64(schema.Status{Tools: []schema.Tool{weeklyOnly}}, true, render.MetricLimit5h)
		if !ok {
			t.Fatal("not ok")
		}
		raw, _ := base64.StdEncoding.DecodeString(b64)
		img, _ := png.Decode(bytes.NewReader(raw))
		var sum int
		b := img.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				_, _, _, a := img.At(x, y).RGBA()
				sum += int(a >> 8)
			}
		}
		return sum
	}
	light, heavy := ink(10), ink(90)
	if light <= heavy {
		t.Errorf("expected the weekly fallback to drive the ring: ink at 10%% used (%d) should exceed 90%% used (%d)", light, heavy)
	}
}

func TestUnavailableRendersTrackOnly(t *testing.T) {
	// An unavailable tool still produces a gauge (dim track + dim logo),
	// so the image dimensions stay stable.
	s := schema.Status{Tools: []schema.Tool{schema.Unavailable(schema.ToolCodex)}}
	b64, ok := PNGBase64(s, true, render.MetricLimit5h)
	if !ok {
		t.Fatal("not ok")
	}
	if w, h := decode(t, b64); w != canvas || h != canvas {
		t.Errorf("dims = %dx%d, want %dx%d", w, h, canvas, canvas)
	}
}
