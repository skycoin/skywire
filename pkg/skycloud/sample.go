// Package skycloud pkg/skycloud/sample.go c0-com-util
package skycloud

import (
	"image"
	"strings"
)

// coverage is how much of a cell, as mean alpha 0-255, must be inside the
// logo for the cell to count. The shape has a soft edge, and anything near
// half speckles the bottom row of the smaller sizes.
const coverage = 190

// Columns is the columns that keep img in proportion at rows rows: its width
// over its height, doubled for a cell twice as tall as it is wide.
func Columns(img image.Image, rows int) int {
	b := img.Bounds()
	return (2*rows*b.Dx() + b.Dy()/2) / b.Dy()
}

// Sample reduces img to a stencil of cols by rows. A '#' is a cell the image
// mostly covers and a space is one it does not. See backdrop.Stencil.
func Sample(img image.Image, cols, rows int) []string {
	b := img.Bounds()
	out := make([]string, rows)
	for y := range rows {
		var line strings.Builder
		for x := range cols {
			x0, x1 := b.Min.X+x*b.Dx()/cols, b.Min.X+(x+1)*b.Dx()/cols
			y0, y1 := b.Min.Y+y*b.Dy()/rows, b.Min.Y+(y+1)*b.Dy()/rows
			if meanAlpha(img, x0, y0, x1, y1) > coverage {
				line.WriteByte('#')
				continue
			}
			line.WriteByte(' ')
		}
		out[y] = line.String()
	}
	return out
}

// meanAlpha is the mean alpha over a rectangle of img, 0-255.
func meanAlpha(img image.Image, x0, y0, x1, y1 int) int {
	var sum, n int64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			sum += int64(a >> 8)
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return int(sum / n)
}

// Trim drops blank rows off the top and bottom and blank columns off both
// sides, so the stencil's extent is the shape's. Placement measures the
// stencil, and padding left in it would push the cloud off center.
func Trim(rows []string) []string {
	for len(rows) > 0 && strings.TrimSpace(rows[0]) == "" {
		rows = rows[1:]
	}
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		return rows
	}
	left, right := len(rows[0]), 0
	for _, r := range rows {
		t := strings.TrimRight(r, " ")
		if t == "" {
			continue
		}
		left = min(left, len(r)-len(strings.TrimLeft(r, " ")))
		right = max(right, len(t))
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		end := min(right, len(r))
		if left >= end {
			continue
		}
		out[i] = strings.TrimRight(r[left:end], " ")
	}
	return out
}
