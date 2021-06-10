package pathutil

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/skycoin/skywire/pkg/util/rename"
)

const (
	tmpSuffix      = ".tmp"
	ownerRWOtherRW = 0606
	userRWXGroupRX = 0750
)

// EnsureDir attempts to create given directory.
func EnsureDir(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.MkdirAll(path, userRWXGroupRX)
	}

	return nil
}

// AtomicWriteFile creates a temp file in which to write data, then calls syscall.Rename to swap it and write it on
// filename for an atomic write.
func AtomicWriteFile(filename string, data []byte) error {
	tempFilePath := filename + tmpSuffix

	if _, err := os.Stat(filename); err == nil {
		if err := os.Remove(filename); err != nil {
			return fmt.Errorf("remove %s: %w", filename, err)
		}
	}

	if _, err := os.Stat(tempFilePath); err == nil {
		if err := os.Remove(filename); err != nil {
			return fmt.Errorf("remove %s: %w", filename, err)
		}
	}

	if err := ioutil.WriteFile(tempFilePath, data, ownerRWOtherRW); err != nil {
		return err
	}

	return rename.Rename(tempFilePath, filename)
}

// AtomicAppendToFile calls AtomicWriteFile but appends new data to destiny file
func AtomicAppendToFile(filename string, data []byte) error {
	oldFile, err := ioutil.ReadFile(filepath.Clean(filename))
	if err != nil {
		return err
	}

	return AtomicWriteFile(filename, append(oldFile, data...))
}
