// Package flags pkg/flags/cloud.go c1-com-util
package flags

import (
	"image"

	"github.com/skycoin/skywire/pkg/skycloud"
)

// cloudGutter is the clear columns between the text's column budget and the
// cloud, so the two never read as one block.
const cloudGutter = 4

// helpCloud is the Skycoin cloud in the columns the help text leaves free. The
// text wraps at HelpWidth, so everything past it and the gutter is free.
func helpCloud() *skycloud.Mask {
	m := skycloud.New()
	m.Box = func(cols, rows int) image.Rectangle {
		return image.Rect(HelpWidth+nameIndent+cloudGutter, 0, cols-skycloud.Margin, rows)
	}
	return m
}
