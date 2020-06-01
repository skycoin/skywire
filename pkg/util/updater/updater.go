// Package updater downloads skywire-visor updates and updates its binary file.
// NOTE: Windows is not supported.
package updater

import (
	"context"
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
	"sync/atomic"
	"time"
	"unicode"

	"github.com/SkycoinProject/dmsg/buildinfo"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/google/go-github/github"
	"github.com/mholt/archiver/v3"
	"github.com/schollz/progressbar/v2"

	"github.com/SkycoinProject/skywire-mainnet/pkg/restart"
	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/rename"
)

const (
	owner             = "SkycoinProject"
	gitProjectName    = "skywire-mainnet"
	projectName       = "skywire"
	releaseURL        = "https://github.com/" + owner + "/" + gitProjectName + "/releases"
	checksumsFilename = "checksums.txt"
	checkSumLength    = 64
	permRWX           = 0755
	exitDelay         = 100 * time.Millisecond
	oldSuffix         = ".old"
	appsSubfolder     = "apps"
	archiveFormat     = ".tar.gz"
	visorBinary       = "skywire-visor"
	hypervisorBinary  = "hypervisor"
	cliBinary         = "skywire-cli"
)

var (
	// ErrNoChecksumFound happens when no checksum is found.
	ErrNoChecksumFound = errors.New("no checksum found")
	// ErrMalformedChecksumFile happens when checksum is malformed.
	ErrMalformedChecksumFile = errors.New("malformed checksum file")
	// ErrAlreadyStarted is returned when updating is already started.
	ErrAlreadyStarted = errors.New("updating already started")
	// ErrTagNameEmpty is returned when tag name is empty.
	ErrTagNameEmpty = errors.New("tag name is empty")
	// ErrUnknownChannel is returned when channel is unknown.
	ErrUnknownChannel = errors.New("channel is unknown")
	// ErrNoReleases is returned when no releases are found.
	ErrNoReleases = errors.New("no releases found")
)

// Updater checks if a new version of skywire is available, downloads its binary files
// and runs them, substituting the current binary files.
type Updater struct {
	log        *logging.Logger
	restartCtx *restart.Context
	appsPath   string
	updating   int32
}

// New returns a new Updater.
func New(log *logging.Logger, restartCtx *restart.Context, appsPath string) *Updater {
	return &Updater{
		log:        log,
		restartCtx: restartCtx,
		appsPath:   appsPath,
	}
}

// UpdateConfig defines a config for updater.
// If a config field is not empty, a default value is overridden.
// Version overrides Channel.
// ArchiveURL/ChecksumURL override Version and channel.
type UpdateConfig struct {
	Channel      Channel `json:"channel"`
	Version      string  `json:"version"`
	ArchiveURL   string  `json:"archive_url"`
	ChecksumsURL string  `json:"checksums_url"`
}

// Channel defines channel for updating.
type Channel string

const (
	// ChannelStable is the latest release.
	ChannelStable = Channel("stable")
	// ChannelTesting is the latest draft, pre-release or release.
	ChannelTesting = Channel("testing")
)

// Update performs an update operation.
// NOTE: Update may call os.Exit.
func (u *Updater) Update(updateConfig *UpdateConfig) (updated bool, err error) {
	if !atomic.CompareAndSwapInt32(&u.updating, 0, 1) {
		return false, ErrAlreadyStarted
	}
	defer atomic.StoreInt32(&u.updating, 0)

	version := updateConfig.Version
	if version == "" {
		latestVersion, err := u.UpdateAvailable(updateConfig.Channel)
		if err != nil {
			return false, fmt.Errorf("failed to get last Skywire version: %w", err)
		}

		// No update is available.
		if latestVersion == nil {
			return false, nil
		}

		version = latestVersion.String()
	}

	u.log.Infof("Update found, version: %q", version)

	downloadedBinariesPath, err := u.download(updateConfig, version)
	if err != nil {
		return false, err
	}

	currentBasePath := filepath.Dir(u.restartCtx.CmdPath())
	if err := u.updateBinaries(downloadedBinariesPath, currentBasePath); err != nil {
		return false, err
	}

	if err := u.restartCurrentProcess(); err != nil {
		currentVisorPath := filepath.Join(currentBasePath, visorBinary)
		oldVisorPath := filepath.Join(downloadedBinariesPath, visorBinary+oldSuffix)

		u.restore(currentVisorPath, oldVisorPath)

		return false, err
	}

	u.removeFiles(downloadedBinariesPath)

	// Let RPC call complete and then exit.
	defer func() {
		if err == nil {
			go u.exitAfterDelay(exitDelay)
		}
	}()

	return true, nil
}

// UpdateAvailable checks if an update is available.
// If it is, the method returns the last available version.
// Otherwise, it returns nil.
func (u *Updater) UpdateAvailable(channel Channel) (*Version, error) {
	u.log.Infof("Looking for updates")

	latestVersion, err := latestVersion(channel)
	if err != nil {
		return nil, err
	}

	u.log.Infof("Last Skywire version: %q", latestVersion.String())

	if !needUpdate(latestVersion) {
		u.log.Infof("You are using the latest version of Skywire")
		return nil, nil
	}

	return latestVersion, nil
}

func (u *Updater) exitAfterDelay(delay time.Duration) {
	time.Sleep(delay)
	u.log.Infof("Exiting")
	os.Exit(0)
}

func (u *Updater) updateBinaries(downloadedBinariesPath string, currentBasePath string) error {
	for _, app := range apps() {
		if err := u.updateBinary(downloadedBinariesPath, u.appsPath, app); err != nil {
			return fmt.Errorf("failed to update %s binary: %w", app, err)
		}
	}

	if err := u.updateBinary(downloadedBinariesPath, currentBasePath, cliBinary); err != nil {
		return fmt.Errorf("failed to update %s binary: %w", cliBinary, err)
	}

	if err := u.updateBinary(downloadedBinariesPath, currentBasePath, visorBinary); err != nil {
		return fmt.Errorf("failed to update %s binary: %w", visorBinary, err)
	}

	if err := u.updateBinary(downloadedBinariesPath, currentBasePath, hypervisorBinary); err != nil {
		return fmt.Errorf("failed to update %s binary: %w", hypervisorBinary, err)
	}

	return nil
}

func (u *Updater) updateBinary(downloadedBinariesPath, basePath, binary string) error {
	downloadedBinaryPath := filepath.Join(downloadedBinariesPath, binary)
	if _, err := os.Stat(downloadedBinaryPath); os.IsNotExist(err) {
		downloadedBinaryPath = filepath.Join(downloadedBinariesPath, appsSubfolder, binary)
	}

	currentBinaryPath := filepath.Join(basePath, binary)
	oldBinaryPath := downloadedBinaryPath + oldSuffix

	if _, err := os.Stat(oldBinaryPath); err == nil {
		if err := os.Remove(oldBinaryPath); err != nil {
			return fmt.Errorf("remove %s: %w", oldBinaryPath, err)
		}
	}

	currentBinaryExists := false
	if _, err := os.Stat(currentBinaryPath); err == nil {
		currentBinaryExists = true

		if err := rename.Rename(currentBinaryPath, oldBinaryPath); err != nil {
			return fmt.Errorf("rename %s to %s: %w", currentBinaryPath, oldBinaryPath, err)
		}
	}

	if err := rename.Rename(downloadedBinaryPath, currentBinaryPath); err != nil {
		// Try to revert previous rename.
		if currentBinaryExists {
			if err := rename.Rename(oldBinaryPath, currentBinaryPath); err != nil {
				u.log.Errorf("Failed to rename file %q to %q: %v", oldBinaryPath, currentBinaryPath, err)
			}
		}

		return fmt.Errorf("rename %s to %s: %w", downloadedBinaryPath, currentBinaryPath, err)
	}

	u.log.Infof("Successfully updated %s binary", binary)
	return nil
}

// restore restores old binary file.
func (u *Updater) restore(currentBinaryPath string, toBeRemoved string) {
	u.removeFiles(currentBinaryPath)

	if _, err := os.Stat(toBeRemoved); err != nil {
		return
	}

	if err := rename.Rename(toBeRemoved, currentBinaryPath); err != nil {
		u.log.Errorf("Failed to rename file %q to %q: %v", toBeRemoved, currentBinaryPath, err)
	}
}

func (u *Updater) download(updateConfig *UpdateConfig, version string) (string, error) {
	checksumsURL := fileURL(version, checksumsFilename)
	if updateConfig.ChecksumsURL != "" {
		checksumsURL = updateConfig.ChecksumsURL
	}

	u.log.Infof("Checksums file URL: %q", checksumsURL)

	checksums, err := downloadChecksums(checksumsURL)
	if err != nil {
		return "", fmt.Errorf("failed to download checksums: %w", err)
	}

	u.log.Infof("Checksums file downloaded")

	archiveFilename := archiveFilename(projectName, version, runtime.GOOS, runtime.GOARCH)
	u.log.Infof("Archive filename: %v", archiveFilename)

	checksum, err := getChecksum(checksums, archiveFilename)
	if err != nil {
		return "", fmt.Errorf("failed to get checksum: %w", err)
	}

	u.log.Infof("Archive checksum should be %q", checksum)

	archiveURL := fileURL(version, archiveFilename)
	if updateConfig.ArchiveURL != "" {
		archiveURL = updateConfig.ArchiveURL
	}

	u.log.Infof("Downloading archive from %q", archiveURL)

	archivePath, err := downloadFile(archiveURL, archiveFilename)
	if err != nil {
		return "", fmt.Errorf("failed to download archive file from URL %q: %w", archiveURL, err)
	}

	u.log.Infof("Downloaded archive file to %q", archivePath)

	valid, err := isChecksumValid(archivePath, checksum)
	if err != nil {
		return "", fmt.Errorf("failed to check file %q sum: %w", archivePath, err)
	}

	if !valid {
		return "", fmt.Errorf("checksum is not valid")
	}

	destPath := filepath.Join(filepath.Dir(archivePath), projectName)

	if _, err := os.Stat(destPath); err == nil {
		u.removeFiles(destPath)
	}

	if err := archiver.Unarchive(archivePath, destPath); err != nil {
		return "", err
	}

	u.removeFiles(archivePath)

	return destPath, nil
}

func (u *Updater) restartCurrentProcess() error {
	u.log.Infof("Starting new file instance")

	if err := u.restartCtx.Start(); err != nil {
		u.log.Errorf("Failed to start binary: %v", err)
		return err
	}

	return nil
}

func (u *Updater) removeFiles(names ...string) {
	for _, name := range names {
		u.log.Infof("Removing file %q", name)
		if err := os.RemoveAll(name); err != nil {
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

	r := io.TeeReader(resp.Body, progressBar(resp.ContentLength))

	data, err := ioutil.ReadAll(r)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func progressBar(contentLength int64) io.Writer {
	newline := progressbar.OptionOnCompletion(func() {
		fmt.Println()
	})

	return progressbar.NewOptions64(contentLength, progressbar.OptionSetBytes64(contentLength), newline)
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

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad HTTP response status code %d", resp.StatusCode)
	}

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

	out := io.MultiWriter(f, progressBar(resp.ContentLength))

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", err
	}

	if err := f.Chmod(permRWX); err != nil {
		return "", err
	}

	if err := f.Close(); err != nil {
		return "", err
	}

	return path, nil
}

func fileURL(version, filename string) string {
	return releaseURL + "/download/" + version + "/" + filename
}

func archiveFilename(file, version, os, arch string) string {
	return file + "-" + version + "-" + os + "-" + arch + archiveFormat
}

func needUpdate(last *Version) bool {
	current, err := currentVersion()
	if err != nil {
		// Unknown versions should be updated.
		return true
	}

	return last.Cmp(current) > 0
}

func latestVersion(channel Channel) (*Version, error) {
	ctx := context.Background()
	client := github.NewClient(nil)

	switch channel {
	case ChannelStable:
		release, _, err := client.Repositories.GetLatestRelease(ctx, owner, gitProjectName)
		if err != nil {
			return nil, err
		}

		if release.TagName == nil {
			return nil, ErrTagNameEmpty
		}

		return VersionFromString(*release.TagName)

	case ChannelTesting:
		releases, _, err := client.Repositories.ListReleases(ctx, owner, gitProjectName, nil)
		if err != nil {
			return nil, err
		}

		if len(releases) == 0 {
			return nil, ErrNoReleases
		}

		// Latest release should be the first one.
		release := releases[0]

		if release.TagName == nil {
			return nil, ErrTagNameEmpty
		}

		return VersionFromString(*release.TagName)

	default:
		return nil, ErrUnknownChannel
	}
}

func currentVersion() (*Version, error) {
	return VersionFromString(buildinfo.Version())
}

func apps() []string {
	return []string{
		skyenv.SkychatName,
		skyenv.SkysocksName,
		skyenv.SkysocksClientName,
		skyenv.VPNServerName,
		skyenv.VPNClientName,
	}
}
