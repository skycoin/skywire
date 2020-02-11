// Package updater downloads skywire-visor updates and updates its binary file.
// NOTE: Windows is not supported.
package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/buildinfo"
)

const (
	owner             = "SkycoinProject"
	repo              = "skywire-mainnet"
	releaseURL        = "https://github.com/" + owner + "/" + repo + "/releases"
	urlText           = "/" + owner + "/" + repo + "/releases/tag/"
	checksumsFilename = "checksums.txt"
	checkSumLength    = 64
	permRWX           = 0755
	exitDelay         = 100 * time.Millisecond
)

var (
	// ErrNoChecksumFound happens when no checksum is found.
	ErrNoChecksumFound = errors.New("no checksum found")
	// ErrMalformedChecksumFile happens when checksum is malformed.
	ErrMalformedChecksumFile = errors.New("malformed checksum file")
)

// Updater checks if a new version of skywire-visor is available, downloads its binary file
// and runs it, substituting the current binary file.
type Updater struct {
	log        *logging.Logger
	restartCtx *restart.Context
}

// New returns a new Updater.
func New(log *logging.Logger, restartCtx *restart.Context) *Updater {
	return &Updater{
		log:        log,
		restartCtx: restartCtx,
	}
}

// Update performs an update operation.
func (u *Updater) Update() error {
	u.log.Infof("Looking for updates")

	lastVersion, err := lastVersion()
	if err != nil {
		return fmt.Errorf("failed to get last visor version: %w", err)
	}

	u.log.Infof("Last visor version: %q", lastVersion.String())

	if !updateAvailable(lastVersion) {
		u.log.Infof("You are using the latest version of visor")
		return nil
	}

	u.log.Infof("Update found, version: %q", lastVersion.String())

	path, err := u.download(lastVersion.String())
	if err != nil {
		return err
	}

	return u.start(path)
}

func (u *Updater) download(version string) (string, error) {
	checksumsURL := fileURL(version, checksumFile(version))
	u.log.Infof("Checksum file URL: %q", checksumsURL)

	checksums, err := downloadChecksums(checksumsURL)
	if err != nil {
		return "", fmt.Errorf("failed to download checksums: %w", err)
	}

	u.log.Infof("Checksums file downloaded")

	binaryFilename := binaryFilename(version, runtime.GOOS, runtime.GOARCH)
	u.log.Infof("Binary filename: %v", binaryFilename)

	checksum, err := getChecksum(checksums, binaryFilename)
	if err != nil {
		return "", fmt.Errorf("failed to get checksum: %w", err)
	}

	u.log.Infof("Binary checksum should be %q", checksum)

	fileURL := fileURL(version, binaryFilename)

	path, err := downloadFile(fileURL, binaryFilename)
	if err != nil {
		return "", fmt.Errorf("failed to download binary file from URL %q: %w", fileURL, err)
	}

	u.log.Infof("Downloaded binary file to %q", path)

	valid, err := isChecksumValid(path, checksum)
	if err != nil {
		return "", fmt.Errorf("failed to check file %q sum: %w", path, err)
	}

	if !valid {
		return "", fmt.Errorf("checksum is not valid")
	}

	return path, nil
}

func (u *Updater) start(path string) error {
	currentBinaryPath := u.restartCtx.CmdPath()

	toBeRemoved, err := u.updateBinary(path, currentBinaryPath)
	if err != nil {
		return fmt.Errorf("failed to update binary: %w", err)
	}

	u.log.Infof("Need to remove file in %q", toBeRemoved)

	defer func() {
		if err == nil {
			go func() {
				time.Sleep(exitDelay)

				u.log.Infof("Removing file in %q", toBeRemoved)

				if err := os.Remove(toBeRemoved); err != nil {
					u.log.Errorf("Failed to remove file %q: %v", toBeRemoved, err)
				}

				u.log.Infof("Exiting")
				os.Exit(0)
			}()
		}
	}()

	u.log.Infof("Starting new file instance")

	if err := u.restartCtx.Start(); err != nil {
		u.log.Errorf("Failed to restart visor: %v", err)

		// Restore old binary file
		if err := os.Remove(currentBinaryPath); err != nil {
			u.log.Errorf("Failed to remove file %q: %v", currentBinaryPath, err)
		}

		if err := os.Rename(toBeRemoved, currentBinaryPath); err != nil {
			u.log.Errorf("Failed to rename file %q to %q: %v", toBeRemoved, currentBinaryPath, err)
		}

		return fmt.Errorf("failed to restart visor: %w", err)
	}

	return nil
}

func (u *Updater) updateBinary(downloadPath, currentPath string) (toBeRemoved string, err error) {
	oldPath := currentPath + ".old"

	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Remove(oldPath); err != nil {
			return "", err
		}
	}

	if err := os.Rename(currentPath, oldPath); err != nil {
		return "", err
	}

	if err := os.Rename(downloadPath, currentPath); err != nil {
		// Try to revert previous os.Rename
		if err := os.Rename(oldPath, currentPath); err != nil {
			u.log.Errorf("Failed to rename file %q to %q: %v", oldPath, currentPath, err)
		}

		return "", err
	}

	return oldPath, nil
}

func isChecksumValid(filename, wantSum string) (bool, error) {
	f, err := os.Open(filepath.Clean(filename))
	if err != nil {
		return false, err
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return false, err
	}

	if err := f.Close(); err != nil {
		return false, err
	}

	gotSum := hex.EncodeToString(hasher.Sum(nil))

	return gotSum == wantSum, nil
}

// NOTE: getChecksum does not support Unicode in checksums file.
func getChecksum(checksums, filename string) (string, error) {
	idx := strings.Index(checksums, filename)
	if idx == -1 {
		return "", ErrNoChecksumFound
	}

	// Remove space(s) separator.
	last := idx
	for last > 0 && unicode.IsSpace(rune(checksums[last-1])) {
		last--
	}

	first := last - checkSumLength

	if first < 0 {
		return "", ErrMalformedChecksumFile
	}

	return checksums[first:last], nil
}

func downloadChecksums(url string) (checksums string, err error) {
	resp, err := http.Get(url) // nolint:gosec
	if err != nil {
		return "", err
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received bad status code: %d", resp.StatusCode)
	}

	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func downloadFile(url, filename string) (path string, err error) {
	resp, err := http.Get(url) // nolint:gosec
	if err != nil {
		return "", err
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	tmpDir := os.TempDir()
	path = filepath.Join(tmpDir, filename)

	if _, err = os.Stat(path); err == nil {
		// File exists
		if err := os.Remove(path); err != nil {
			return "", err
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}

	if err := f.Chmod(permRWX); err != nil {
		return "", err
	}

	return path, nil
}

func fileURL(version string, filename string) string {
	return releaseURL + "/download/" + version + "/" + filename
}

func checksumFile(version string) string {
	return "skywire-visor-" + version + "-" + checksumsFilename
}

func binaryFilename(version string, os, arch string) string {
	return "skywire-visor-" + version + "-" + os + "-" + arch
}

func updateAvailable(last *Version) bool {
	current, err := currentVersion()
	if err != nil {
		// Unknown versions should be updated.
		return true
	}

	return last.Cmp(current) > 0
}

func lastVersion() (*Version, error) {
	html, err := lastVersionHTML()
	if err != nil {
		return nil, err
	}

	return VersionFromString(extractLastVersion(string(html)))
}

func currentVersion() (*Version, error) {
	return VersionFromString(buildinfo.Version())
}

func lastVersionHTML() (data []byte, err error) {
	resp, err := http.Get(releaseURL)
	if err != nil {
		return nil, err
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()

	return ioutil.ReadAll(resp.Body)
}

func extractLastVersion(buffer string) string {
	// First occurrence is the latest version.
	idx := strings.Index(buffer, urlText)
	if idx == -1 {
		return ""
	}

	versionWithRest := buffer[idx+len(urlText):]

	idx = strings.Index(versionWithRest, `"`)
	if idx == -1 {
		return versionWithRest
	}

	return versionWithRest[:idx]
}
