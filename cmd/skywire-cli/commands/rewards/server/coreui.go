// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/ui.go
package clirewardsserver

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bitfield/script"
)

func extractFiles() (string, error) {
	tempDir := os.TempDir() + "/ui"
	err := os.Mkdir(tempDir, 0750)
	if err != nil {
		toRemove, err := script.FindFiles(tempDir).Reject(tempDir + "/vendor").Slice()
		if err != nil {
			return "", err
		}
		for i := range toRemove {
			_, err := os.Stat(toRemove[i])
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			err = os.RemoveAll(toRemove[i])
			if err != nil {
				return "", err
			}
		}
		log.Println("Omitted vendor dir when cleaning directory:", tempDir)
		log.Println("Directory contents:")
		_, _ = script.FindFiles(tempDir).Stdout() //nolint:errcheck,gosec
	}

	if err := fs.WalkDir(embeddedFiles, "ui", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("failed to access path %s: %w", path, err)
		}
		relPath, err := filepath.Rel("ui", path)
		if err != nil {
			return fmt.Errorf("failed to calculate relative path for %s: %w", path, err)
		}
		outputPath := filepath.Join(tempDir, relPath)
		if d.IsDir() {
			return os.MkdirAll(outputPath, 0750)
		}
		content, err := embeddedFiles.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}
		if err := os.WriteFile(outputPath, content, 0600); err != nil {
			return fmt.Errorf("failed to write file %s: %w", outputPath, err)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to extract files: %w", err)
	}
	return tempDir, nil
}
