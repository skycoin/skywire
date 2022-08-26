// Package rename rename.go
package rename

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
)

const crossDeviceError = "invalid cross-device link"

// Rename renames (moves) oldPath to newPath using os.Rename.
// If paths are located on different drives or filesystems, os.Rename fails.
// In that case, Rename uses a workaround by copying oldPath to newPath and removing oldPath thereafter.
func Rename(oldPath, newPath string) error {
	if err := os.Rename(oldPath, newPath); err == nil || !strings.Contains(err.Error(), crossDeviceError) {
		return err
	}

	stat, err := os.Stat(oldPath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	if !stat.Mode().IsRegular() {
		return fmt.Errorf("is regular: %w", err)
	}

	// Paths are located on different devices.
	if err := move(oldPath, newPath); err != nil {
		return fmt.Errorf("move: %w", err)
	}

	if err := os.Chmod(newPath, stat.Mode()); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}

	if err := os.Remove(oldPath); err != nil {
		return fmt.Errorf("remove: %w", err)
	}

	return nil
}

func move(oldPath string, newPath string) error {
	inputFile, err := os.Open(oldPath) // nolint:gosec
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}

	defer func() {
		if err := inputFile.Close(); err != nil {
			log.Printf("Failed to close file %q: %v", inputFile.Name(), err)
		}
	}()

	outputFile, err := os.Create(newPath) // nolint:gosec
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}

	defer func() {
		if err := outputFile.Close(); err != nil {
			log.Printf("Failed to close file %q: %v", outputFile.Name(), err)
		}
	}()

	if _, err = io.Copy(outputFile, inputFile); err != nil {
		return fmt.Errorf("copy: %w", err)
	}

	return nil
}
