//go:build android || ios || js

package wastebasket

func Trash(paths ...string) error {
	return ErrPlatformNotSupported
}

func Empty() error {
	return ErrPlatformNotSupported
}
