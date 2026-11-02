//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// gitRoot: finds rootdir of this package
func gitRoot(path string) (string, error) {
	// normalize the path
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	for {
		fi, err := os.Stat(filepath.Join(path, ".git"))
		if err == nil {
			if !fi.IsDir() {
				return "", fmt.Errorf(".git exist but is not a directory")
			}
			return filepath.Dir(filepath.Join(path, ".git")), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			// unknown error
			return "", err
		}

		// detect bare repo
		ok, err := isGitDir(path)
		if err != nil {
			return "", err
		}
		if ok {
			return path, nil
		}

		parent := filepath.Dir(path)
		if parent == path {
			return "", fmt.Errorf(".git not found")
		}

		path = parent
	}
}

func isGitDir(path string) (bool, error) {
	markers := []string{"HEAD", "objects", "refs"}

	for _, marker := range markers {
		_, err := os.Stat(filepath.Join(path, marker))
		if err == nil {
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			// unknown error
			return false, err
		}
		return false, nil
	}

	return true, nil
}
