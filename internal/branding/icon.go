package branding

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
)

//go:embed logo.png
var logo []byte

var source = func() image.Image {
	im, err := png.Decode(bytes.NewReader(logo))
	if err != nil {
		panic(err)
	}
	return im
}()

// PNG uses one approved logo for the tray, window and executable resources.
// Average premultiplied color and alpha together to preserve transparent edges.
func PNG(size int) []byte {
	b := source.Bounds()
	if size < 1 || size > b.Dx() || size > b.Dy() {
		panic("invalid icon size")
	}
	// Windows ICO resources use 8-bit RGBA channels (32 bits per pixel).
	icon := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var red, green, blue, alpha, count uint64
			for sy := y * b.Dy() / size; sy < (y+1)*b.Dy()/size; sy++ {
				for sx := x * b.Dx() / size; sx < (x+1)*b.Dx()/size; sx++ {
					r, g, bl, a := source.At(b.Min.X+sx, b.Min.Y+sy).RGBA()
					red += uint64(r)
					green += uint64(g)
					blue += uint64(bl)
					alpha += uint64(a)
					count++
				}
			}
			icon.Set(x, y, color.RGBA64{uint16(red / count), uint16(green / count), uint16(blue / count), uint16(alpha / count)})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, icon); err != nil {
		panic(err)
	}
	return out.Bytes()
}
