//go:generate go run assets_generate.go

package runtime

import "github.com/pgavlin/femto"

var Files = femto.NewRuntimeFiles(files)
