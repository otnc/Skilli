package icon

import (
	"bytes"
	"image/png"
	"testing"
)

func TestPNGDecodesAtRequestedSize(t *testing.T) {
	for _, size := range []int{32, 1024} {
		data, err := PNG(size, false)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Fatalf("size %d: got %v", size, b)
		}
	}
}

func TestDesignAtLargeSize(t *testing.T) {
	img := Image(1024, false)
	for _, tc := range []struct {
		name string
		x, y int
		want [4]uint8
	}{
		{"transparent corner", 4, 4, [4]uint8{0, 0, 0, 0}},
		{"body", 512, 300, [4]uint8{26, 94, 111, 255}},
		{"gauge fill", 300, 600, [4]uint8{134, 232, 190, 255}},
		{"empty slot", 700, 600, [4]uint8{250, 253, 252, 255}},
	} {
		c := img.NRGBAAt(tc.x, tc.y)
		if got := [4]uint8{c.R, c.G, c.B, c.A}; got != tc.want {
			t.Errorf("%s at (%d,%d) = %v; want %v", tc.name, tc.x, tc.y, got, tc.want)
		}
	}
}

func TestLowVariantIsAmberWithFullerGauge(t *testing.T) {
	img := Image(1024, true)
	if c := img.NRGBAAt(512, 300); c.R != 217 || c.G != 119 {
		t.Fatalf("body = %v; want amber", c)
	}
	if c := img.NRGBAAt(600, 600); c.R != 254 || c.G != 226 {
		t.Fatalf("gauge at x=600 = %v; want the low fill", c)
	}
}

func TestEdgesAreAntiAliased(t *testing.T) {
	img := Image(32, false)
	partial := false
	for y := range 32 {
		for x := range 32 {
			if a := img.NRGBAAt(x, y).A; a > 0 && a < 255 {
				partial = true
			}
		}
	}
	if !partial {
		t.Fatal("rounded corners must have partially covered pixels")
	}
}
