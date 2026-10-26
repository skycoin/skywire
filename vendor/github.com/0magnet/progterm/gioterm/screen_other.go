//go:build cgo && unix && !((linux || freebsd || openbsd) && !android && !nox11)

package gioterm

import "image"

// screen is unknown here: zero, at 1 pixel to a dp.
func screen() (image.Point, float32) { return image.Point{}, 1 }
