// Package menubar renders the SwiftBar menu bar image: each tool's logo
// ringed by an iOS-app-download-style progress ring whose fill tracks the
// 5-hour rate-limit usage. Output is a monochrome PNG (template image) so
// the menu bar tints it for light/dark automatically.
package menubar

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"math"

	"github.com/kosako/tachograph/internal/schema"
)

const (
	ss     = 4  // supersampling factor for anti-aliasing
	canvas = 44 // logical pixels per tool (square)
	gap    = 8  // logical pixels between tools
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

	rOut := c * 0.47
	thick := c * 0.085
	rIn := rOut - thick
	rLogo := rIn - c*0.045

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
	if t.Tool == schema.ToolClaudeCode {
		drawClaude(img, cx, cy, rLogo, logoA)
	} else {
		drawCodex(img, cx, cy, rLogo, logoA)
	}
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

// drawClaude approximates the Claude/Anthropic sunburst: tapered radial spokes.
func drawClaude(img *image.Alpha, cx, cy, r float64, a uint8) {
	const spokes = 12
	half := r * 0.085 // spoke half-width at the rim
	for i := 0; i < spokes; i++ {
		ang := float64(i) / spokes * 2 * math.Pi
		x0 := cx + r*0.18*math.Cos(ang)
		y0 := cy + r*0.18*math.Sin(ang)
		x1 := cx + r*0.92*math.Cos(ang)
		y1 := cy + r*0.92*math.Sin(ang)
		drawTaperedLine(img, x0, y0, x1, y1, r*0.03, half, a)
	}
}

// drawCodex approximates a hexagonal knot to distinguish Codex/OpenAI.
func drawCodex(img *image.Alpha, cx, cy, r float64, a uint8) {
	const sides = 6
	rr := r * 0.82
	half := r * 0.075
	pt := func(i int) (float64, float64) {
		ang := float64(i)/sides*2*math.Pi - math.Pi/2
		return cx + rr*math.Cos(ang), cy + rr*math.Sin(ang)
	}
	for i := 0; i < sides; i++ {
		x0, y0 := pt(i)
		x1, y1 := pt((i + 1) % sides)
		drawTaperedLine(img, x0, y0, x1, y1, half, half, a)
	}
	// inner spokes to every other vertex for a knotted look
	for i := 0; i < sides; i += 2 {
		x1, y1 := pt(i)
		drawTaperedLine(img, cx, cy, x1, y1, half*0.7, half*0.7, a)
	}
}

// drawTaperedLine fills a line whose half-width runs from h0 (at p0) to h1
// (at p1), giving spokes that taper.
func drawTaperedLine(img *image.Alpha, x0, y0, x1, y1, h0, h1 float64, a uint8) {
	minX := int(math.Floor(math.Min(x0, x1) - math.Max(h0, h1) - 1))
	maxX := int(math.Ceil(math.Max(x0, x1) + math.Max(h0, h1) + 1))
	minY := int(math.Floor(math.Min(y0, y1) - math.Max(h0, h1) - 1))
	maxY := int(math.Ceil(math.Max(y0, y1) + math.Max(h0, h1) + 1))
	dx, dy := x1-x0, y1-y0
	ll := dx*dx + dy*dy
	for py := minY; py <= maxY; py++ {
		for px := minX; px <= maxX; px++ {
			fx := float64(px) + 0.5
			fy := float64(py) + 0.5
			var tt float64
			if ll > 0 {
				tt = ((fx-x0)*dx + (fy-y0)*dy) / ll
			}
			if tt < 0 {
				tt = 0
			}
			if tt > 1 {
				tt = 1
			}
			projX := x0 + tt*dx
			projY := y0 + tt*dy
			d := math.Hypot(fx-projX, fy-projY)
			if d <= h0+(h1-h0)*tt {
				setMax(img, px, py, a)
			}
		}
	}
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
