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
		if err != nil && latestVersion == nil {
			logger.WithError(err).Fatal("Failed to get latest version of Skywire from servers.")
			return
		} else if err == nil && latestVersion == nil {
			logger.Info("Great! Your visor has the latest version of Skywire.")
			return
		}

		// Checking visor build tag. skywire-cli update command just work on skybian images.
		visor, err := rpcClient().Summary()
		if err != nil {
			logger.WithError(err).Fatal("Failed to get build tag of visor.")
			return
		}

		if visor.BuildTag != "skybian" {
			logger.Warn("Update from skywire-cli just available for Skybian images. Use source code or package managers to update your Skywire.")
			return
		}

		// Updating
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
	},
}
