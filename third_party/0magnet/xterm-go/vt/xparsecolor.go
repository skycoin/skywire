package vt

import (
	"fmt"
	"strconv"
	"strings"
)

// Port of src/common/input/XParseColor.ts — parse color specs of OSC
// 4/10/11/12 (see `man xparsecolor`).
//
// Supported formats:
//   - rgb:<red>/<green>/<blue> with <red>, <green>, <blue> in h|hh|hhh|hhhh
//   - #RGB, #RRGGBB, #RRRGGGBBB, #RRRRGGGGBBBB

// ParseColor parses a color spec to 8-bit-per-channel RGB. ok is false
// for unsupported specs (rgbi:, named colors).
func ParseColor(data string) (rgb [3]int, ok bool) {
	if data == "" {
		return rgb, false
	}
	low := strings.ToLower(data)
	if strings.HasPrefix(low, "rgb:") {
		parts := strings.Split(low[4:], "/")
		if len(parts) != 3 {
			return rgb, false
		}
		width := len(parts[0])
		if width < 1 || width > 4 || len(parts[1]) != width || len(parts[2]) != width {
			return rgb, false
		}
		base := float64(uint32(1)<<(4*width) - 1)
		for i, p := range parts {
			v, err := strconv.ParseUint(p, 16, 32)
			if err != nil {
				return rgb, false
			}
			rgb[i] = int(float64(v)/base*255 + 0.5)
		}
		return rgb, true
	}
	if strings.HasPrefix(low, "#") {
		low = low[1:]
		n := len(low)
		if n != 3 && n != 6 && n != 9 && n != 12 {
			return rgb, false
		}
		adv := n / 3
		for i := 0; i < 3; i++ {
			c, err := strconv.ParseUint(low[adv*i:adv*i+adv], 16, 32)
			if err != nil {
				return rgb, false
			}
			switch adv {
			case 1:
				rgb[i] = int(c << 4)
			case 2:
				rgb[i] = int(c)
			case 3:
				rgb[i] = int(c >> 4)
			default:
				rgb[i] = int(c >> 8)
			}
		}
		return rgb, true
	}
	return rgb, false
}

// pad hex output to the requested bit width.
func padHex(n int, bits int) string {
	s := fmt.Sprintf("%02x", n)
	switch bits {
	case 4:
		return s[:1]
	case 8:
		return s
	case 12:
		return (s + s)[:3]
	default:
		return s + s
	}
}

// ToRgbString converts a color to an rgb:../../.. string of bits depth
// (default use: 16).
func ToRgbString(color [3]int, bits int) string {
	return "rgb:" + padHex(color[0], bits) + "/" + padHex(color[1], bits) + "/" + padHex(color[2], bits)
}
