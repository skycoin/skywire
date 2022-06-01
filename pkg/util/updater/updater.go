// Package updater downloads skywire-visor updates and updates its binary file.
// NOTE: Windows is not supported.
package updater

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"os/user"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/google/go-github/github"
	"github.com/bitfield/script"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/zcalusic/sysinfo"

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
		return false, errors.New("updating already started")
	}
	defer atomic.StoreInt32(&u.updating, 0)

	current, err := user.Current()
	if err != nil {
		u.status.Set("failed checking permissions")
		u.log.Error("failed checking permissions")
		return false, errors.New("failed checking permissions")
	}
	if current.Uid != "0" {
		u.status.Set("insufficient permissions")
		u.log.Error("insufficient permissions")
		return false, errors.New("insufficient permissions")
	}

	var si sysinfo.SysInfo
	si.GetSysInfo()
	if (si.OS.Vendor != "debian") && (si.OS.Vendor != "arch") {
		u.status.Set(fmt.Sprintf("updates not supported on this operating system: %s", si.OS.Vendor))
		return false, errors.New("operating system not supported")
	}

	if (si.OS.Vendor == "debian") {
		u.status.Set("Checking for available package")
		available, err := script.Exec(`apt-cache search skywire-bin`).String()
		if err != nil {
			return false, err
		}
		if (available == "") || (available == "\n") {
			return false, errors.New("Repository not configured or skywire not available")
		}
		u.status.Set("syncing package database")
		if _, err := script.Exec(`sudo apt update`).String(); err != nil {
			u.log.Error("error syncing package database")
			return false, err
		}
		u.log.Info("synced package database")
		u.log.Info("installing skywire-bin")
		if _, err := script.Exec(`sudo NOAUTOCONFIG=true apt install skywire-bin -y`).String(); err != nil {
			u.log.Error("error installing skywire-bin")
			return false, err
		}
		u.log.Info("installed skywire-bin")
		u.log.Info("updating config and restarting visor")
		if _, err := script.Exec(`DMSGPTYTERM=true skywire-autoconfig`).String(); err != nil {
			u.log.Error("error running skywire-autoconfig")
			return false, err
		}
		//this may not return before the process is restarted
		return true, nil
	}
	if (si.OS.Vendor == "arch") {
		u.status.Set("not yet implemented")
		u.log.Error("not yet implemented")
		return false, errors.New("not yet implemented")
	}
	return false, nil
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
