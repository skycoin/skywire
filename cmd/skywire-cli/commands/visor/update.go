package visor

import (
	"github.com/skycoin/skywire/pkg/util/updater"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(updateCmd)
}

var channelType string

func init() {
	updateCmd.Flags().StringVar(&channelType, "channel", "stable", channelType)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Obtains summary of visor information",
	Run: func(_ *cobra.Command, _ []string) {
		// Set channel for check/get available update
		channel := updater.ChannelStable
		if channelType == "testing" {
			channel = updater.ChannelTesting
		}

		// Get the latest version of skywire on for update
		latestVersion, err := rpcClient().UpdateAvailable(channel)
		if err != nil || latestVersion == nil {
			logger.WithError(err).Fatal("Failed to get latest version of Skywire from servers.")
		}

		// Get the version of current Skywire on visor
		visor, err := rpcClient().Summary()
		currentVersion := visor.Overview.BuildInfo.Version
		if err != nil {
			logger.WithError(err).Fatal("Failed to get current version of Skywire.")
		}

		// Checking visor build tag. skywire-cli update command just work on skybian images.
		if visor.BuildTag != "skybian" {
			logger.Warn("Update from skywire-cli just available for Skybian images. Use source code or package managers to update your Skywire.")
			return
		}

		// Comparing current version and latest version of Skywire.
		if currentVersion < latestVersion.String() {
			logger.Infof("New version of Skywire available. %s", latestVersion.String())
			updaterConfig := updater.UpdateConfig{
				Version: latestVersion.String(),
			}
			logger.Info("Updating...")

			if _, err := rpcClient().Update(updaterConfig); err != nil {
				logger.WithError(err).Warn("Failed to update visor.")
				return
			}
			logger.Info("Update completed!")
		} else {
			logger.Info("Great! Your visor has the latest version of Skywire.")
			return
		}
	},
}
