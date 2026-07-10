package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"sync"
)

// The green and the <70/70-89/>=90 thresholds are derived from
// Artzainnn/ClaudeUsageBar (MIT; see LICENSE) so this bar reads alike at a
// glance. The yellow and red are Apple system colours; the orange (service
// status: major) and grey (idle) states are this app's own additions.
var (
	colorOK    = color.NRGBA{0x22, 0xC4, 0x5E, 0xFF} // < 70%
	colorWarn  = color.NRGBA{0xFF, 0xCC, 0x00, 0xFF} // 70-89%
	colorMajor = color.NRGBA{0xFF, 0x95, 0x00, 0xFF} // service status: major
	colorAlert = color.NRGBA{0xFF, 0x3B, 0x30, 0xFF} // >= 90%
	colorIdle  = color.NRGBA{0x8E, 0x8E, 0x93, 0xFF} // no reading yet
)

func usageColor(pct float64) color.NRGBA {
	switch {
	case pct >= 90:
		return colorAlert
	case pct >= 70:
		return colorWarn
	default:
		return colorOK
	}
}

// statusColor maps a Statuspage indicator onto the same palette.
func statusColor(indicator string) color.NRGBA {
	switch indicator {
	case indicatorNone:
		return colorOK
	case indicatorMinor:
		return colorWarn
	case indicatorMajor:
		return colorMajor
	case indicatorCritical:
		return colorAlert
	default:
		return colorIdle
	}
}

// sparkPoints is the eight-pointed sparkle, its geometry taken point-for-point
// from Artzainnn/ClaudeUsageBar (MIT; see LICENSE), as the SVG path
// "M8 1 L9 6 L13 3 L10 7 L15 8 L10 9 L13 13 L9 10 L8 15 L7 10 L3 13 L6 9 L1 8 L6 7 L3 3 L7 6 Z"
// on a 16x16 box. It is symmetric about both axes, so the SVG's y-down
// convention needs no flip for image/draw.
var sparkPoints = [][2]float64{
	{8, 1}, {9, 6}, {13, 3}, {10, 7}, {15, 8}, {10, 9}, {13, 13}, {9, 10},
	{8, 15}, {7, 10}, {3, 13}, {6, 9}, {1, 8}, {6, 7}, {3, 3}, {7, 6},
}

const (
	iconBox   = 16 // points; the size AppKit renders a status-item image at
	iconScale = 4  // rasterize @4x so it stays crisp on Retina
	subSample = 4  // NxN coverage samples per pixel, for antialiasing
)

func sparkIcon(c color.NRGBA) []byte {
	return cachedIcon("spark", c, insideSpark)
}

// dotIcon is the 8pt leading dot on each dropdown usage row.
func dotIcon(c color.NRGBA) []byte {
	return cachedIcon("dot", c, insideDot)
}

// blankIcon is a transparent glyph so reset rows get the same NSImageLeft inset
// as the dotted rows above, aligning each block; padding with spaces can't match it.
func blankIcon() []byte {
	return cachedIcon("blank", color.NRGBA{}, func(x, y float64) bool { return false })
}

func insideSpark(x, y float64) bool { return inPolygon(sparkPoints, x, y) }

func insideDot(x, y float64) bool {
	dx, dy := x-8, y-8
	return dx*dx+dy*dy <= 4*4
}

var (
	iconMu    sync.Mutex
	iconCache = map[string][]byte{}
)

func cachedIcon(kind string, c color.NRGBA, inside func(x, y float64) bool) []byte {
	key := kind + string([]byte{c.R, c.G, c.B})
	iconMu.Lock()
	defer iconMu.Unlock()
	if b, ok := iconCache[key]; ok {
		return b
	}
	b := encodePNG(rasterize(c, inside))
	iconCache[key] = b
	return b
}

// rasterize antialiases `inside` over the 16x16 box, subSample^2 samples per pixel.
func rasterize(c color.NRGBA, inside func(x, y float64) bool) *image.NRGBA {
	size := iconBox * iconScale
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const samples = subSample * subSample
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			hits := 0
			for sy := 0; sy < subSample; sy++ {
				for sx := 0; sx < subSample; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/subSample) / iconScale
					y := (float64(py) + (float64(sy)+0.5)/subSample) / iconScale
					if inside(x, y) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{c.R, c.G, c.B, uint8(hits * 255 / samples)})
		}
	}
	return img
}

// inPolygon is a crossing-number test against a closed simple polygon.
func inPolygon(pts [][2]float64, x, y float64) bool {
	in := false
	for i, j := 0, len(pts)-1; i < len(pts); j, i = i, i+1 {
		xi, yi := pts[i][0], pts[i][1]
		xj, yj := pts[j][0], pts[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err) // encoding an in-memory NRGBA to a bytes.Buffer cannot fail
	}
	return buf.Bytes()
}
