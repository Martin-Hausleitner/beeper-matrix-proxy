package beepersource

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"strings"
)

func platformLogoPNG(platform string, bgHex string) ([]byte, bool) {
	if pngBytes, ok := brandAppIconPNG(platform, bgHex); ok {
		return pngBytes, true
	}
	bg := parseHexColor(bgHex)
	fg := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "whatsapp":
		fillCircle(img, 128, 122, 72, fg)
		fillCircle(img, 128, 122, 55, bg)
		fillTriangle(img, image.Pt(82, 185), image.Pt(101, 163), image.Pt(113, 179), fg)
		drawLine(img, 104, 99, 119, 142, 17, fg)
		drawLine(img, 119, 142, 154, 158, 17, fg)
		drawLine(img, 103, 98, 117, 93, 13, fg)
		drawLine(img, 155, 158, 166, 145, 13, fg)
	case "signal":
		fillCircle(img, 128, 128, 72, fg)
		fillCircle(img, 128, 128, 55, bg)
		for angle := 0.0; angle < 360; angle += 28 {
			x := 128 + math.Cos(angle*math.Pi/180)*72
			y := 128 + math.Sin(angle*math.Pi/180)*72
			fillCircle(img, int(x), int(y), 6, fg)
		}
		fillCircle(img, 128, 121, 40, fg)
		fillRect(img, 111, 151, 145, 166, fg)
		fillTriangle(img, image.Pt(105, 164), image.Pt(127, 154), image.Pt(112, 183), fg)
	case "telegram":
		fillTriangle(img, image.Pt(54, 120), image.Pt(201, 63), image.Pt(168, 197), fg)
		fillTriangle(img, image.Pt(106, 134), image.Pt(174, 84), image.Pt(143, 151), bg)
		fillTriangle(img, image.Pt(106, 134), image.Pt(140, 153), image.Pt(128, 185), fg)
	case "discord":
		fillRect(img, 70, 89, 186, 162, fg)
		fillCircle(img, 91, 122, 35, fg)
		fillCircle(img, 165, 122, 35, fg)
		fillCircle(img, 107, 128, 11, bg)
		fillCircle(img, 149, 128, 11, bg)
	case "instagram":
		strokeRoundRect(img, 68, 68, 188, 188, 28, 12, fg)
		strokeCircle(img, 128, 128, 32, 12, fg)
		fillCircle(img, 164, 92, 10, fg)
	case "messenger":
		fillCircle(img, 128, 123, 72, fg)
		fillTriangle(img, image.Pt(91, 178), image.Pt(75, 211), image.Pt(120, 187), fg)
		fillTriangle(img, image.Pt(73, 142), image.Pt(119, 99), image.Pt(109, 137), bg)
		fillTriangle(img, image.Pt(110, 137), image.Pt(147, 157), image.Pt(120, 99), bg)
		fillTriangle(img, image.Pt(147, 157), image.Pt(184, 110), image.Pt(158, 149), bg)
	case "imessage":
		fillCircle(img, 128, 121, 72, fg)
		fillTriangle(img, image.Pt(91, 176), image.Pt(76, 210), image.Pt(120, 187), fg)
	case "x":
		drawLine(img, 78, 67, 180, 189, 24, fg)
		drawLine(img, 178, 67, 76, 189, 20, fg)
	case "linkedin":
		fillRect(img, 67, 103, 95, 190, fg)
		fillCircle(img, 81, 76, 16, fg)
		fillRect(img, 114, 103, 142, 190, fg)
		fillRect(img, 141, 130, 178, 190, fg)
		fillCircle(img, 160, 130, 28, fg)
	case "matrix", "beeper", "beeper (matrix)", "bridgev2":
		fillRect(img, 70, 60, 88, 196, fg)
		fillRect(img, 168, 60, 186, 196, fg)
		fillRect(img, 106, 86, 124, 170, fg)
		fillRect(img, 132, 86, 150, 170, fg)
		fillRect(img, 120, 113, 136, 131, fg)
	case "slack":
		fillRect(img, 72, 104, 184, 134, fg)
		fillRect(img, 72, 148, 184, 178, fg)
		fillRect(img, 104, 72, 134, 184, fg)
		fillRect(img, 148, 72, 178, 184, fg)
	default:
		return nil, false
	}

	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes(), true
}

func brandAppIconPNG(platform string, bgHex string) ([]byte, bool) {
	icon, ok := brandIconByKey(platform)
	if !ok {
		return nil, false
	}
	body, ok := brandIconPNGByKey(icon.Key)
	if !ok {
		return nil, false
	}
	glyph, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	bg := parseHexColor(firstNonEmpty(icon.BrandColor, bgHex))
	img := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	scaled := image.NewRGBA(image.Rect(0, 0, 150, 150))
	drawFit(scaled, glyph)
	for y := 0; y < scaled.Bounds().Dy(); y++ {
		for x := 0; x < scaled.Bounds().Dx(); x++ {
			blendAt(img, x+53, y+53, scaled.At(x, y))
		}
	}
	var out bytes.Buffer
	_ = png.Encode(&out, img)
	return out.Bytes(), true
}

func parseHexColor(raw string) color.RGBA {
	value := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if len(value) != 6 {
		return color.RGBA{R: 86, G: 98, B: 246, A: 255}
	}
	n, err := strconv.ParseUint(value, 16, 32)
	if err != nil {
		return color.RGBA{R: 86, G: 98, B: 246, A: 255}
	}
	return color.RGBA{R: uint8(n >> 16), G: uint8(n >> 8), B: uint8(n), A: 255}
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	draw.Draw(img, image.Rect(x0, y0, x1, y1), &image.Uniform{C: c}, image.Point{}, draw.Src)
}

func fillCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	rr := r * r
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= rr && image.Pt(x, y).In(img.Bounds()) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func strokeCircle(img *image.RGBA, cx, cy, r, width int, c color.RGBA) {
	outer := r * r
	inner := (r - width) * (r - width)
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			d := (x-cx)*(x-cx) + (y-cy)*(y-cy)
			if d <= outer && d >= inner && image.Pt(x, y).In(img.Bounds()) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}

func strokeRoundRect(img *image.RGBA, x0, y0, x1, y1, radius, width int, c color.RGBA) {
	fillRect(img, x0+radius, y0, x1-radius, y0+width, c)
	fillRect(img, x0+radius, y1-width, x1-radius, y1, c)
	fillRect(img, x0, y0+radius, x0+width, y1-radius, c)
	fillRect(img, x1-width, y0+radius, x1, y1-radius, c)
	strokeCircle(img, x0+radius, y0+radius, radius, width, c)
	strokeCircle(img, x1-radius, y0+radius, radius, width, c)
	strokeCircle(img, x0+radius, y1-radius, radius, width, c)
	strokeCircle(img, x1-radius, y1-radius, radius, width, c)
}

func fillTriangle(img *image.RGBA, a, b, cpt image.Point, c color.RGBA) {
	minX := min(a.X, min(b.X, cpt.X))
	maxX := max(a.X, max(b.X, cpt.X))
	minY := min(a.Y, min(b.Y, cpt.Y))
	maxY := max(a.Y, max(b.Y, cpt.Y))
	area := edge(a, b, cpt)
	if area == 0 {
		return
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			p := image.Pt(x, y)
			w0 := edge(b, cpt, p)
			w1 := edge(cpt, a, p)
			w2 := edge(a, b, p)
			if (w0 >= 0 && w1 >= 0 && w2 >= 0) || (w0 <= 0 && w1 <= 0 && w2 <= 0) {
				if p.In(img.Bounds()) {
					img.SetRGBA(x, y, c)
				}
			}
		}
	}
}

func edge(a, b, c image.Point) int {
	return (c.X-a.X)*(b.Y-a.Y) - (c.Y-a.Y)*(b.X-a.X)
}

func drawLine(img *image.RGBA, x0, y0, x1, y1, width int, c color.RGBA) {
	minX := min(x0, x1) - width
	maxX := max(x0, x1) + width
	minY := min(y0, y1) - width
	maxY := max(y0, y1) + width
	dx := float64(x1 - x0)
	dy := float64(y1 - y0)
	lengthSq := dx*dx + dy*dy
	if lengthSq == 0 {
		fillCircle(img, x0, y0, width/2, c)
		return
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			t := ((float64(x-x0) * dx) + (float64(y-y0) * dy)) / lengthSq
			t = math.Max(0, math.Min(1, t))
			px := float64(x0) + t*dx
			py := float64(y0) + t*dy
			if math.Hypot(float64(x)-px, float64(y)-py) <= float64(width)/2 && image.Pt(x, y).In(img.Bounds()) {
				img.SetRGBA(x, y, c)
			}
		}
	}
}
