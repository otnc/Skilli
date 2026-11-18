// Package icon draws Skilli's icon, a drive with a fill gauge, at any size.
// The tray icon and the application icon both come from here, so the two
// cannot drift apart.
package icon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// The design is laid out on a 1024-unit square and scaled to the size asked for.
const design = 1024

var (
	slotColor = color.NRGBA{R: 250, G: 253, B: 252, A: 255}
	normal    = palette{body: color.NRGBA{R: 26, G: 94, B: 111, A: 255}, fill: color.NRGBA{R: 134, G: 232, B: 190, A: 255}, fillEnd: 512}
	// low is amber with a fuller gauge, so a low drive shows in the tray
	// without reading the tooltip.
	low = palette{body: color.NRGBA{R: 217, G: 119, B: 6, A: 255}, fill: color.NRGBA{R: 254, G: 226, B: 226, A: 255}, fillEnd: 704}
)

type palette struct {
	body, fill color.NRGBA
	fillEnd    float64
}

type roundRect struct {
	x0, y0, x1, y1, r float64
}

var (
	body = roundRect{64, 160, 960, 864, 120}
	slot = roundRect{192, 512, 832, 704, 48}
)

func (r roundRect) contains(x, y float64) bool {
	if x < r.x0 || x > r.x1 || y < r.y0 || y > r.y1 {
		return false
	}
	cx := min(max(x, r.x0+r.r), r.x1-r.r)
	cy := min(max(y, r.y0+r.r), r.y1-r.r)
	return (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r.r*r.r
}

// Image draws the icon as a size×size image with a transparent background.
// lowSpace selects the amber variant shown while a drive is low.
func Image(size int, lowSpace bool) *image.NRGBA {
	colors := normal
	if lowSpace {
		colors = low
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := float64(design) / float64(size)
	paint(img, scale, body.contains, colors.body)
	paint(img, scale, slot.contains, slotColor)
	// The gauge keeps the slot's rounded left end but has a square right
	// end, so it reads as a level rather than a toggle switch.
	paint(img, scale, func(x, y float64) bool { return x < colors.fillEnd && slot.contains(x, y) }, colors.fill)
	return img
}

// PNG encodes Image as PNG.
//
//	data, err := icon.PNG(32, false)
func PNG(size int, lowSpace bool) ([]byte, error) {
	var out bytes.Buffer
	if err := png.Encode(&out, Image(size, lowSpace)); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// paint composites c over every pixel by the share of its 4×4 samples that
// fall inside the shape, which smooths the edges at small sizes.
func paint(img *image.NRGBA, scale float64, inside func(x, y float64) bool, c color.NRGBA) {
	const samples = 4
	bounds := img.Bounds()
	for py := bounds.Min.Y; py < bounds.Max.Y; py++ {
		for px := bounds.Min.X; px < bounds.Max.X; px++ {
			hits := 0
			for sy := range samples {
				for sx := range samples {
					x := (float64(px) + (float64(sx)+0.5)/samples) * scale
					y := (float64(py) + (float64(sy)+0.5)/samples) * scale
					if inside(x, y) {
						hits++
					}
				}
			}
			if hits > 0 {
				img.SetNRGBA(px, py, over(c, float64(hits)/(samples*samples), img.NRGBAAt(px, py)))
			}
		}
	}
}

// over composites c with coverage a on top of dst, both non-premultiplied.
func over(c color.NRGBA, a float64, dst color.NRGBA) color.NRGBA {
	dstA := float64(dst.A) / 255
	outA := a + dstA*(1-a)
	channel := func(src, d uint8) uint8 {
		return uint8(math.Round((float64(src)*a + float64(d)*dstA*(1-a)) / outA))
	}
	return color.NRGBA{R: channel(c.R, dst.R), G: channel(c.G, dst.G), B: channel(c.B, dst.B), A: uint8(math.Round(outA * 255))}
}
