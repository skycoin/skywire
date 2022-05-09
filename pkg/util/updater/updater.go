// Package updater downloads skywire-visor updates and updates its binary file.
// NOTE: Windows is not supported.
package updater

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync/atomic"

	"github.com/google/go-github/github"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/restart"
)

const (
	owner          = "skycoin"
	gitProjectName = "skywire"
	releaseURL     = "https://github.com/" + owner + "/" + gitProjectName + "/releases"
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
	// ErrUnknownTarget is returned when target is unknown.
	ErrUnknownTarget = errors.New("target is unknown")
	// ErrNoReleases is returned when no releases are found.
	ErrNoReleases = errors.New("no releases found")
	// ErrRateLimit is returned when rate limited happened.
	ErrRateLimit = errors.New("rate limiting for checking update exceeded")
)

// Updater checks if a new version of skywire is available, downloads its binary files
// and runs them, substituting the current binary files.
type Updater struct {
	log        *logging.Logger
	restartCtx *restart.Context
	appsPath   string
	updating   int32
	status     *status
}

// New returns a new Updater.
func New(log *logging.Logger, restartCtx *restart.Context, appsPath string) *Updater {
	return &Updater{
		log:        log,
		restartCtx: restartCtx,
		appsPath:   appsPath,
		status:     newStatus(),
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
	ChannelStable Channel = "stable"
	// ChannelTesting is the latest draft, pre-release or release.
	ChannelTesting Channel = "testing"
)

// Update performs an update operation.
// NOTE: Update may call os.Exit.
func (u *Updater) Update(updateConfig UpdateConfig) (updated bool, err error) {
	if !atomic.CompareAndSwapInt32(&u.updating, 0, 1) {
		return false, ErrAlreadyStarted
	}
	defer atomic.StoreInt32(&u.updating, 0)

	u.status.Set("Started, checking update")

	version, err := u.getVersion(updateConfig)
	if err != nil {
		return false, err
	}

	// No update is available.
	if version == "" {
		return false, nil
	}

	u.status.Set(fmt.Sprintf("Found version %q", version))

	u.status.Set(fmt.Sprintf("Checking/Adding repo %s", "https://deb.skywire.skycoin.com/")) // add if not exist
	if err := u.addRepo(); err != nil {
		return false, err
	}

	u.status.Set("Update repositories")
	if err := u.aptUpdate(); err != nil {
		return false, err
	}
	u.log.Info("Updating repositories by 'apt update' compeleted.")

	// uninstall current installed skywire if its version is equal or lower that 0.4.2
	currentVersion, err := currentVersion()
	if err != nil {
		return false, err
	}
	if currentVersion.Minor == 4 && currentVersion.Patch <= 2 {
		u.status.Set("Uninstall current skywire version") // if needed
		if err := u.aptRemove(); err != nil {
			return false, err
		}
		u.log.Info("Uninstalling old version compeleted.")
	}

	u.status.Set(fmt.Sprintf("Installing Skywire %q", version))
	if err := u.aptInstall(); err != nil {
		return false, err
	}
	u.log.Info("Installing new version compeleted.")

	u.status.Set("Updating completed. Running autoconfig script and restart services.")
	u.log.Info("Updating completed. Running autoconfig script and restart services.")

	go u.runningAutoconfig()

	return true, nil
}

// Status returns status of the current update operation.
// An empty string is returned if no operation is running.
func (u *Updater) Status() string {
	return u.status.Get()
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

func (u *Updater) getVersion(updateConfig UpdateConfig) (string, error) {
	version := updateConfig.Version
	if version == "" {
		latestVersion, err := u.UpdateAvailable(updateConfig.Channel)
		if err != nil {
			return "", fmt.Errorf("failed to get last Skywire version: %w", err)
		}

		// No update is available.
		if latestVersion == nil {
			return "", nil
		}

		version = latestVersion.String()
	}

	u.log.Infof("Update found, version: %q", version)

	return version, nil
}

func (u *Updater) addRepo() error {
	output, _ := exec.Command("bash", "-c", "cat /etc/apt/sources.list | grep https://deb.skywire.skycoin.com").Output() //nolint

	if len(output) == 0 {
		if err := exec.Command("bash", "-c", "echo 'deb https://deb.skywire.skycoin.com sid main' | sudo tee -a /etc/apt/sources.list").Run(); err != nil {
			u.log.Error("Get error during add repository")
			return err
		}
		if err := exec.Command("bash", "-c", "curl -L https://deb.skywire.skycoin.com/KEY.asc | sudo apt-key add -").Run(); err != nil {
			u.log.Error("Get error during add key")
			return err
		}
		u.log.Info("Repository added")
	} else {
		u.log.Info("Repository exist")
	}
	return nil
}

func (u *Updater) aptUpdate() error {
	if err := exec.Command("bash", "-c", "sudo apt update").Run(); err != nil {
		u.log.Error("Get error during update apt repositories")
		return err
	}
	return nil
}

func (u *Updater) aptRemove() error {
	if err := exec.Command("bash", "-c", "sudo apt remove skywire-bin -y").Run(); err != nil {
		u.log.Error("Get error during remove skywire-bin package")
		return err
	}
	return nil
}

func (u *Updater) aptInstall() error {
	if err := exec.Command("bash", "-c", "sudo NOAUTOCONFIG=true apt install skywire-bin -y").Run(); err != nil {
		u.log.Error("Get error during installing skywire-bin package")
		return err
	}
	return nil
}

func (u *Updater) runningAutoconfig() {
	if err := exec.Command("bash", "-c", "sleep 5s ; sudo skywire-autoconfig").Process.Release(); err != nil {
		u.log.Error("Get error during installing skywire-bin package")
	}
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
		release, resp, err := client.Repositories.GetLatestRelease(ctx, owner, gitProjectName)
		if err != nil {
			if resp.StatusCode == 403 {
				return nil, ErrRateLimit
			}
			return nil, err
		}

		if release.TagName == nil {
			return nil, ErrTagNameEmpty
		}

		return VersionFromString(*release.TagName)

	case ChannelTesting:
		releases, resp, err := client.Repositories.ListReleases(ctx, owner, gitProjectName, nil)
		if err != nil {
			if resp.StatusCode == 403 {
				return nil, ErrRateLimit
			}
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
