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
	oldSuffix         = ".old"
	visorBinary       = "skywire-visor"
	cliBinary         = "skywire-cli"
)

var (
	// ErrNoChecksumFound happens when no checksum is found.
	ErrNoChecksumFound = errors.New("no checksum found")
	// ErrMalformedChecksumFile happens when checksum is malformed.
	ErrMalformedChecksumFile = errors.New("malformed checksum file")
)

// Updater checks if a new version of skywire is available, downloads its binary files
// and runs them, substituting the current binary files.
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
// NOTE: Update may call os.Exit.
func (u *Updater) Update() error {
	u.log.Infof("Looking for updates")

	lastVersion, err := lastVersion()
	if err != nil {
		return fmt.Errorf("failed to get last Skywire version: %w", err)
	}

	u.log.Infof("Last Skywire version: %q", lastVersion.String())

	if !updateAvailable(lastVersion) {
		u.log.Infof("You are using the latest version of Skywire")
		return nil
	}

	u.log.Infof("Update found, version: %q", lastVersion.String())

	downloadedVisorPath, err := u.download(visorBinary, lastVersion.String())
	if err != nil {
		return err
	}

	downloadedCLIPath, err := u.download(cliBinary, lastVersion.String())
	if err != nil {
		return err
	}

	currentVisorPath := u.restartCtx.CmdPath()
	currentCLIPath := cliPath(currentVisorPath)

	oldCLIPath := downloadedCLIPath + oldSuffix
	oldVisorPath := downloadedVisorPath + oldSuffix

	if err := u.updateBinary(downloadedCLIPath, currentCLIPath, oldCLIPath); err != nil {
		return fmt.Errorf("failed to update %s binary: %w", cliBinary, err)
	}

	if err := u.updateBinary(downloadedVisorPath, currentVisorPath, oldVisorPath); err != nil {
		return fmt.Errorf("failed to update %s binary: %w", visorBinary, err)
	}

	if err := u.restartCurrentProcess(); err != nil {
		u.restore(currentVisorPath, oldVisorPath)
		return err
	}

	u.removeFiles(oldVisorPath, oldCLIPath)

	u.log.Infof("Exiting")
	os.Exit(0)

	// Unreachable.
	return nil
}

// restore restores old binary file.
func (u *Updater) restore(currentBinaryPath string, toBeRemoved string) {
	u.removeFiles(currentBinaryPath)

	if err := os.Rename(toBeRemoved, currentBinaryPath); err != nil {
		u.log.Errorf("Failed to rename file %q to %q: %v", toBeRemoved, currentBinaryPath, err)
	}
}

func (u *Updater) download(binaryName, version string) (string, error) {
	checksumsURL := fileURL(version, checksumFile(binaryName, version))
	u.log.Infof("Checksum file URL: %q", checksumsURL)

	checksums, err := downloadChecksums(checksumsURL)
	if err != nil {
		return "", fmt.Errorf("failed to download checksums: %w", err)
	}

	u.log.Infof("Checksums file downloaded")

	binaryFilename := binaryFilename(binaryName, version, runtime.GOOS, runtime.GOARCH)
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

func (u *Updater) restartCurrentProcess() error {
	u.log.Infof("Starting new file instance")

	if err := u.restartCtx.Start(); err != nil {
		u.log.Errorf("Failed to start binary: %v", err)
		return err
	}

	return nil
}

func (u *Updater) updateBinary(downloadPath, currentPath, oldPath string) error {
	if _, err := os.Stat(oldPath); err == nil {
		if err := os.Remove(oldPath); err != nil {
			return err
		}
	}

	if err := os.Rename(currentPath, oldPath); err != nil {
		return err
	}

	if err := os.Rename(downloadPath, currentPath); err != nil {
		// Try to revert previous os.Rename
		if err := os.Rename(oldPath, currentPath); err != nil {
			u.log.Errorf("Failed to rename file %q to %q: %v", oldPath, currentPath, err)
		}

		return err
	}

	return nil
}

func (u *Updater) removeFiles(names ...string) {
	for _, name := range names {
		if err := os.Remove(name); err != nil {
			u.log.Infof("Removing file %q", name)
			u.log.Errorf("Failed to remove file %q: %v", name, err)
		}
	}
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

func fileURL(version, filename string) string {
	return releaseURL + "/download/" + version + "/" + filename
}

func checksumFile(binaryName, version string) string {
	return binaryName + "-" + version + "-" + checksumsFilename
}

func binaryFilename(binaryName, version, os, arch string) string {
	return binaryName + "-" + version + "-" + os + "-" + arch
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

func cliPath(visorPath string) string {
	return filepath.Join(filepath.Dir(visorPath), cliBinary)
}
