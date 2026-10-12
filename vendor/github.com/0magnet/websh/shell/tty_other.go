//go:build !unix && !(js && wasm)

package shell

import (
	"errors"
	"os"
)

// blockingDup has no terminal to share off unix: a program is run by the
// interpreter's own exec instead.
func blockingDup(*os.File) (*os.File, error) {
	return nil, errors.New("no terminal sharing on this platform")
}

func restoreNonblock(*os.File) {}
