// Package menubar renders the SwiftBar menu bar image: each tool's logo
// ringed by an iOS-app-download-style progress ring whose fill tracks the
// 5-hour rate-limit usage. Output is a monochrome PNG (template image) so
// the menu bar tints it for light/dark automatically.
package menubar

import (
	"bytes"
	"embed"
	"encoding/base64"
	"image"
	"image/draw"
	"image/png"
	"math"

	"github.com/kosako/tachograph/internal/schema"
)

// Logo marks, rasterized from assets/logos/*.svg to monochrome PNGs (see
// the README's contributing notes). Embedded so the runtime stays
// stdlib-only — no SVG rasterizer dependency.
//
//go:embed assets/claude.png assets/codex.png
var assetFS embed.FS

var (
	claudeLogo = mustLoadLogo("assets/claude.png")
	codexLogo  = mustLoadLogo("assets/codex.png")
)

func mustLoadLogo(name string) *image.NRGBA {
	f, err := assetFS.Open(name)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		panic(err)
	}
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), src, b.Min, draw.Src)
	return out
}

const (
	ss     = 4  // supersampling factor for anti-aliasing
	canvas = 44 // logical pixels per tool (square)
	gap    = 2  // logical pixels between tools (gauges already carry margin)
)

// alpha levels in the supersampled buffer (averaged down to gray coverage).
const (
	aTrack = 55  // unfilled ring track
	aFill  = 255 // filled ring portion
	aStale = 150 // filled ring when data is stale
	aLogo  = 235 // logo mark
	aDim   = 80  // logo mark when the tool is unavailable
)

// PNGBase64 renders the gauges for the status and returns a base64 PNG plus
// ok=false when there is nothing to draw.
func PNGBase64(s schema.Status) (string, bool) {
	if len(s.Tools) == 0 {
		return "", false
	}
	n := len(s.Tools)
	w := canvas*n + gap*(n-1)
	big := image.NewAlpha(image.Rect(0, 0, w*ss, canvas*ss))
	for i, t := range s.Tools {
		drawGauge(big, (canvas+gap)*i*ss, t)
	}
	img := downsample(big, w, canvas)

	var buf bytes.Buffer
	if png.Encode(&buf, img) != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// drawGauge renders one tool into a canvas*ss square at horizontal offset ox
// (supersampled coordinates).
func drawGauge(img *image.Alpha, ox int, t schema.Tool) {
	c := float64(canvas * ss)
	cx := float64(ox) + c/2
	cy := c / 2

	// Generous transparent margin keeps the gauge from looking oversized
	// next to system menu bar icons (~56% of the canvas).
	rOut := c * 0.28
	thick := c * 0.042
	rIn := rOut - thick
	rLogo := rIn - c*0.025

	pct, hasPct := fiveHourFrac(t)
	fillA := aFill
	if t.Stale {
		fillA = aStale
	}

	// Progress ring: full circle, starting at 12 o'clock, clockwise.
	for py := 0; py < canvas*ss; py++ {
		for px := ox; px < ox+canvas*ss; px++ {
			dx := float64(px) + 0.5 - cx
			dy := float64(py) + 0.5 - cy
			dist := math.Hypot(dx, dy)
			if dist < rIn || dist > rOut {
				continue
			}
			a := math.Atan2(dy, dx) * 180 / math.Pi // [-180,180], 0 = right
			if a < 0 {
				a += 360
			}
			frac := math.Mod(a-270+360, 360) / 360 // 0 at top, clockwise
			switch {
			case hasPct && frac <= pct:
				setMax(img, px, py, uint8(fillA))
			default:
				setMax(img, px, py, aTrack)
			}
		}
	}

	logoA := uint8(aLogo)
	if !t.Available || t.Error != nil {
		logoA = aDim
	}
	logo := codexLogo
	if t.Tool == schema.ToolClaudeCode {
		logo = claudeLogo
	}
	drawLogo(img, cx, cy, rLogo*0.85, logo, logoA) // a little inset from the ring
}

// drawLogo composites the embedded logo (centered, fit to a 2r box) into the
// gauge using its alpha channel as coverage, bilinearly sampled and tinted to
// the given intensity.
func drawLogo(img *image.Alpha, cx, cy, r float64, logo *image.NRGBA, intensity uint8) {
	side := 2 * r
	x0 := cx - r
	y0 := cy - r
	sb := logo.Bounds()
	sw, sh := float64(sb.Dx()), float64(sb.Dy())
	n := int(math.Ceil(side))
	for dy := 0; dy <= n; dy++ {
		for dx := 0; dx <= n; dx++ {
			u := (float64(dx) + 0.5) / side * sw
			v := (float64(dy) + 0.5) / side * sh
			a := sampleAlpha(logo, u, v)
			if a == 0 {
				continue
			}
			cov := uint8(float64(a) * float64(intensity) / 255)
			setMax(img, int(x0)+dx, int(y0)+dy, cov)
		}
	}
}

// sampleAlpha bilinearly samples the alpha channel of src at (u,v) pixel
// coordinates, returning 0 outside bounds.
func sampleAlpha(src *image.NRGBA, u, v float64) uint8 {
	b := src.Bounds()
	x0 := int(math.Floor(u - 0.5))
	y0 := int(math.Floor(v - 0.5))
	fx := u - 0.5 - float64(x0)
	fy := v - 0.5 - float64(y0)
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= b.Dx() || y >= b.Dy() {
			return 0
		}
		return float64(src.Pix[src.PixOffset(b.Min.X+x, b.Min.Y+y)+3])
	}
	top := at(x0, y0)*(1-fx) + at(x0+1, y0)*fx
	bot := at(x0, y0+1)*(1-fx) + at(x0+1, y0+1)*fx
	return uint8(top*(1-fy) + bot*fy)
}

func fiveHourFrac(t schema.Tool) (float64, bool) {
	if !t.Available || t.Error != nil {
		return 0, false
	}
	for _, l := range t.Limits {
		if l.Window == schema.WindowFiveHour && l.UsedPct != nil {
			p := *l.UsedPct / 100
			if p < 0 {
				p = 0
			}
			if p > 1 {
				p = 1
			}
			return p, true
		}
	}
	return 0, false
}

func setMax(img *image.Alpha, x, y int, a uint8) {
	if !(image.Pt(x, y).In(img.Bounds())) {
		return
	}
	i := img.PixOffset(x, y)
	if a > img.Pix[i] {
		img.Pix[i] = a
	}
}

// downsample box-averages the supersampled coverage buffer into a black
// template image: RGB stays 0, the averaged coverage becomes alpha so the
// menu bar can tint it for light/dark.
func downsample(big *image.Alpha, w, h int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var sum int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					sum += int(big.Pix[big.PixOffset(x*ss+sx, y*ss+sy)])
				}
			}
			out.Pix[out.PixOffset(x, y)+3] = uint8(sum / (ss * ss)) // alpha only; RGB=0 (black)
		}
	}
	return out
}
