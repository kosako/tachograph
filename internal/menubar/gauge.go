// Package menubar renders the SwiftBar menu bar image: each tool's logo
// ringed by an iOS-app-download-style progress ring whose fill tracks the
// 5-hour rate-limit usage. The ring is colored by usage (green/yellow/red),
// so the output is a full-color PNG. Because a colored image can't be tinted
// by the menu bar, the logo and track follow the system appearance (white on
// dark, near-black on light) to stay legible on both.
package menubar

import (
	"bytes"
	"embed"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"

	"github.com/kosako/tachograph/internal/render"
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
	ss     = 6  // supersampling factor for anti-aliasing
	canvas = 22 // final pixels per tool — SwiftBar shows the image near 1:1,
	gap    = 2  // so this is roughly the on-screen size (menu-bar-icon sized)
)

// Ring pressure colors (match the CLI / dropdown palette).
var (
	cGreen  = color.NRGBA{52, 199, 89, 255}
	cYellow = color.NRGBA{255, 204, 0, 255}
	cRed    = color.NRGBA{255, 59, 48, 255}
	cGray   = color.NRGBA{142, 142, 147, 255}
)

// palette holds the appearance-dependent inks for the logo and unfilled track.
type palette struct {
	logo  color.NRGBA
	track color.NRGBA
}

func paletteFor(dark bool) palette {
	// The track is kept very faint so a low-usage gauge reads as "white logo
	// + a little colored arc", not a dominant gray ring.
	if dark {
		return palette{logo: color.NRGBA{255, 255, 255, 255}, track: color.NRGBA{255, 255, 255, 32}}
	}
	return palette{logo: color.NRGBA{40, 40, 42, 255}, track: color.NRGBA{40, 40, 40, 38}}
}

const aDimLogo = 90 // logo alpha when the tool is unavailable

// PNGBase64 renders the gauges and returns a base64 PNG plus ok=false when
// there is nothing to draw. dark selects the system appearance so the logo
// and track stay legible; metric selects which value drives the ring.
func PNGBase64(s schema.Status, dark bool, metric string) (string, bool) {
	if len(s.Tools) == 0 {
		return "", false
	}
	pal := paletteFor(dark)
	n := len(s.Tools)
	w := canvas*n + gap*(n-1)
	big := image.NewNRGBA(image.Rect(0, 0, w*ss, canvas*ss))
	for i, t := range s.Tools {
		drawGauge(big, (canvas+gap)*i*ss, t, pal, metric)
	}
	img := downsample(big, w, canvas)

	var buf bytes.Buffer
	if png.Encode(&buf, img) != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), true
}

// ringColor is the fill color for the used portion: gray when stale,
// otherwise green/yellow/red by 5h pressure.
func ringColor(t schema.Tool, frac float64) color.NRGBA {
	if t.Stale {
		return cGray
	}
	switch {
	case frac >= 0.8:
		return cRed
	case frac >= 0.5:
		return cYellow
	default:
		return cGreen
	}
}

// drawGauge renders one tool into a canvas*ss square at horizontal offset ox
// (supersampled coordinates). metric selects which value fills the ring.
func drawGauge(img *image.NRGBA, ox int, t schema.Tool, pal palette, metric string) {
	c := float64(canvas * ss)
	cx := float64(ox) + c/2
	cy := c / 2

	// Gauge fills ~88% of the (already small) canvas, leaving a little
	// margin so it sits like a normal menu bar glyph.
	rOut := c * 0.44
	thick := c * 0.065
	rIn := rOut - thick
	rLogo := rIn - c*0.03

	frac, _ := render.Metric(t, metric)
	var pct float64
	hasPct := frac != nil
	if hasPct {
		pct = *frac
	}
	fill := ringColor(t, pct)

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
			if hasPct && frac <= pct {
				setPix(img, px, py, fill)
			} else {
				setPix(img, px, py, pal.track)
			}
		}
	}

	ink := pal.logo
	if !t.Available || t.Error != nil {
		ink.A = aDimLogo
	}
	logo := codexLogo
	if t.Tool == schema.ToolClaudeCode {
		logo = claudeLogo
	}
	drawLogo(img, cx, cy, rLogo*0.85, logo, ink) // a little inset from the ring
}

// drawLogo composites the embedded logo (centered, fit to a 2r box) into the
// gauge, bilinearly sampling its alpha as coverage and painting it in ink.
func drawLogo(img *image.NRGBA, cx, cy, r float64, logo *image.NRGBA, ink color.NRGBA) {
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
			cov := uint8(float64(a) * float64(ink.A) / 255)
			setPix(img, int(x0)+dx, int(y0)+dy, color.NRGBA{ink.R, ink.G, ink.B, cov})
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

// setPix overwrites a supersampled pixel. Gauge materials (ring band vs logo
// region, fill vs track by angle) don't overlap, so plain assignment is fine;
// anti-aliasing comes from supersampling + downsample.
func setPix(img *image.NRGBA, x, y int, col color.NRGBA) {
	if !(image.Pt(x, y).In(img.Bounds())) {
		return
	}
	i := img.PixOffset(x, y)
	img.Pix[i] = col.R
	img.Pix[i+1] = col.G
	img.Pix[i+2] = col.B
	img.Pix[i+3] = col.A
}

// downsample box-averages the supersampled color buffer, premultiplying by
// alpha so edge pixels blend without dark fringes.
func downsample(big *image.NRGBA, w, h int) *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b, a int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					i := big.PixOffset(x*ss+sx, y*ss+sy)
					A := int(big.Pix[i+3])
					r += int(big.Pix[i]) * A
					g += int(big.Pix[i+1]) * A
					b += int(big.Pix[i+2]) * A
					a += A
				}
			}
			o := out.PixOffset(x, y)
			if a > 0 {
				out.Pix[o] = uint8(r / a)
				out.Pix[o+1] = uint8(g / a)
				out.Pix[o+2] = uint8(b / a)
				out.Pix[o+3] = uint8(a / (ss * ss))
			}
		}
	}
	return out
}
