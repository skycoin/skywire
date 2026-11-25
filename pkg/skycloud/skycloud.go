// Package skycloud pkg/skycloud/skycloud.go c0-com-util
//
// Package skycloud is the Skycoin cloud made of code rain: skywire's help
// screen backdrop, and the same picture full screen or in a video.
//
// The cloud is not drawn over the rain but made of it. Cells inside the
// silhouette are lit to a floor with the glyph the rain already holds there
// and turned Skycoin blue, so the mark is the rain's own alphabet falling
// through it. Mask does that to a termanim backdrop. Scene drives it with
// either rain: termanim's live simulation, or matrix/loop's rain that repeats
// exactly, for a clip that loops.
//
// It imports termanim and the standard library only, so a program can use it
// without linking anything else from skywire. internal/leafdeps holds it to
// that.
package skycloud

import (
	"bytes"
	_ "embed" // the logo
	"image"
	"image/png"
)

//go:generate go run ./gencloud skycoin-cloud.png shapes.go

// LogoPNG is the cloud the shapes are sampled from: the icon half of the logo
// skycoin/skycoin serves in its block explorer, wordmark dropped, trimmed to
// the mark. Only its alpha is meaningful.
//
//go:embed skycoin-cloud.png
var LogoPNG []byte

// Logo decodes LogoPNG.
func Logo() (image.Image, error) { return png.Decode(bytes.NewReader(LogoPNG)) }

// Blue is the logo's own fill, #0072ff.
var Blue = [3]int32{0x00, 0x72, 0xff}
