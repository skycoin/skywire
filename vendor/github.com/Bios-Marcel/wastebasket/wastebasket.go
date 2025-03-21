// Package wastebasket allows you to interact with your system trashbin.
package wastebasket

import "errors"

// ErrPlatformNotSupported indicates that the current platform does not suport trashing files.
var ErrPlatformNotSupported = errors.New("platform not supported")
