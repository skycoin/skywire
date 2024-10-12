// Package clirewards cmd/skywire-cli/commands/rewards/services.go
package clirewards

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"syscall"
	"text/template"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"
)

var (
	userName   string
	workingDir string
	skyenvConf string
	outputPath string
)

func init() {
	RootCmd.AddCommand(
		systemdServicesCmd,
	)
	currentDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	fileInfo, err := os.Stat(currentDir)
	if err != nil {
		log.Fatal(err)
	}
	stat := fileInfo.Sys().(*syscall.Stat_t)
	owner, err := user.LookupId(fmt.Sprint(stat.Uid))
	if err != nil {
		log.Fatal(err)
	}

	systemdServicesCmd.Flags().StringVarP(&userName, "user", "u", owner.Username, "user to set - should have write permission on path")
	systemdServicesCmd.Flags().StringVarP(&workingDir, "path", "p", currentDir, "reward system data dir path")
	systemdServicesCmd.Flags().StringVarP(&skyenvConf, "skyenv", "s", "fr.conf", "env config file path")
	systemdServicesCmd.Flags().StringVarP(&outputPath, "out", "o", "/etc/systemd/system", "path to output systemd services")
}

var systemdServicesCmd = &cobra.Command{
	Use:   "systemd",
	Short: "set up systemd services for reward system",
	Long: `set up systemd services for reward system
must be run with sufficient permissions to write to output path`,
	Run: func(_ *cobra.Command, _ []string) {
		// Get the current user

		// Prepare the data for the template
		serviceConfig := svcConfig{
			User: userName,
			Dir:  workingDir,
			Conf: skyenvConf,
		}

		// Create a new template and parse the service file template into it
		tmpl, err := template.New("").Parse(skywireRewardSvcTpl)
		if err != nil {
			log.Fatal(err)
		}

		var renderedServiceFile bytes.Buffer
		err = tmpl.Execute(&renderedServiceFile, serviceConfig)
		if err != nil {
			log.Fatal(err)
		}

		_, err = script.Echo(renderedServiceFile.String()).Tee().WriteFile(outputPath + "/skywire-reward.service")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Wrote to:" + outputPath + "/skywire-reward.service")

		// Create a new template and parse the service file template into it
		tmpl, err = template.New("").Parse(fiberRewardSvcTpl)
		if err != nil {
			log.Fatal(err)
		}

		// Execute the template with the data and output the result to stdout
		err = tmpl.Execute(&renderedServiceFile, serviceConfig)
		if err != nil {
			log.Fatal(err)
		}

		_, err = script.Echo(renderedServiceFile.String()).Tee().WriteFile(outputPath + "/fiberreward.service")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Wrote to:" + outputPath + "/fiberreward.service")

		_, err = script.Echo(skywireRewardTimerTpl).Tee().WriteFile(outputPath + "/skywire-reward.timer")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("Wrote to:" + outputPath + "/skywire-reward.timer")

	},
}

// Timer for log collection & reward calculation

const skywireRewardTimerTpl = `[Unit]
Description=skywire reward timer
After=network.target

[Timer]
OnUnitActiveSec=1h
Unit=skywire-reward.service

[Install]
WantedBy=multi-user.target
`

// Log Collection & reward calculation
const skywireRewardSvcTpl = `
[Unit]
Description=skywire reward service
After=network.target

[Service]
Type=simple
User={{.User}}
WorkingDirectory={{.Dir}}
ExecStart=/bin/bash -c './getlogs.sh && ./reward.sh ; exit 0'

[Install]
WantedBy=multi-user.target

`

// UI / Frontend
const fiberRewardSvcTpl = `
[Unit]
Description=skywire cli rewards ui
After=network.target

[Service]
Type=simple
User={{.User}}
WorkingDirectory={{.Dir}}
Environment={{.Conf}}
ExecStart=/usr/bin/bash -c 'skywire cli rewards ui'
Restart=always
RestartSec=20
TimeoutSec=30

[Install]
WantedBy=multi-user.target

`

type svcConfig struct {
	User string
	Dir  string
	Conf string
}
