package static

import "embed"

//go:embed *
var uiAssets embed.FS

func UIAssets() embed.FS {
	return uiAssets
}
