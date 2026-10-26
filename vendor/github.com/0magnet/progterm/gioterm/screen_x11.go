//go:build cgo && (linux || freebsd || openbsd) && !android && !nox11

package gioterm

/*
#cgo linux pkg-config: x11
#cgo freebsd openbsd LDFLAGS: -L/usr/X11R6/lib -L/usr/local/lib -lX11
#include <stdlib.h>
#include <X11/Xlib.h>
*/
import "C"

import (
	"image"
	"os"
	"strconv"
	"strings"
)

// screen is the X screen's size in pixels and the pixels to a dp Gio will
// use there (Xft.dpi over 96), or zero and 1 without an X display.
func screen() (image.Point, float32) {
	if os.Getenv("DISPLAY") == "" {
		return image.Point{}, 1
	}
	d := C.XOpenDisplay(nil)
	if d == nil {
		return image.Point{}, 1
	}
	defer C.XCloseDisplay(d)
	s := C.XDefaultScreen(d)
	size := image.Pt(int(C.XDisplayWidth(d, s)), int(C.XDisplayHeight(d, s)))
	scale := float32(1)
	if rm := C.XResourceManagerString(d); rm != nil {
		for _, line := range strings.Split(C.GoString(rm), "\n") {
			if v, ok := strings.CutPrefix(line, "Xft.dpi:"); ok {
				if dpi, err := strconv.ParseFloat(strings.TrimSpace(v), 32); err == nil && dpi > 0 {
					scale = float32(dpi / 96)
				}
			}
		}
	}
	return size, scale
}
