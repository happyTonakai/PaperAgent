package systray

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

var iconData []byte

func init() {
	img := drawPDFIcon(32, 32)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic("failed to encode PDF icon: " + err.Error())
	}
	iconData = buf.Bytes()
}

// drawPDFIcon draws a 32x32 PDF document style icon.
// Red rectangle with white header area and folded corner.
func drawPDFIcon(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	red := color.RGBA{0xE7, 0x4C, 0x3C, 0xFF}
	white := color.RGBA{0xFF, 0xFF, 0xFF, 0xFF}
	darkRed := color.RGBA{0xC0, 0x39, 0x2B, 0xFF}

	// Fill red background
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, red)
		}
	}

	// White document header area (top portion)
	headerBottom := h * 10 / 32
	for y := 0; y < headerBottom; y++ {
		for x := 2; x < w; x++ {
			img.Set(x, y, white)
		}
	}

	// Folded corner at bottom-right (small triangle)
	foldSize := 8
	for y := h - foldSize; y < h; y++ {
		for x := w - foldSize; x < w; x++ {
			if x+w-y > w+foldSize-2 {
				img.Set(x, y, darkRed)
			}
		}
	}

	// White triangle fold effect
	for y := h - foldSize; y < h; y++ {
		for x := w - foldSize; x < w; x++ {
			diag := x + y - (w - foldSize) - (h - foldSize)
			if diag > 1 && x+w-y > w+foldSize-2 {
				img.Set(x, y, white)
			}
		}
	}

	return img
}
