// Package cliutil cmd/skywire-cli/commands/util/cloud.go c4-vis-cli
package cliutil

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/spf13/cobra"

	"github.com/0magnet/termanim/canvas"

	"github.com/skycoin/skywire/pkg/skycloud"
)

var cloudCmd = cloudCmdNew()

func cloudCmdNew() *cobra.Command {
	var (
		cloudLoop   time.Duration
		cloudSeed   int64
		cloudBright bool
	)
	cmd := &cobra.Command{
		Use:   "cloud",
		Short: "Play the Skycoin cloud in the code rain full screen",
		Long:  "Play the Skycoin cloud in the code rain full screen until q, Escape or Ctrl-C.",
		Args:  cobra.NoArgs,
	}
	cmd.Flags().DurationVar(&cloudLoop, "loop", 0, "repeat the rain exactly every this long (e.g. 8s) instead of the live rain")
	cmd.Flags().Int64Var(&cloudSeed, "seed", 0, "rain seed; 0 takes one from the clock")
	cmd.Flags().BoolVar(&cloudBright, "bright", false, "tint by each cell's brightest channel, which shows the cloud more")
	cmd.RunE = func(_ *cobra.Command, _ []string) error {
		seed := cloudSeed
		if seed == 0 {
			seed = time.Now().UnixNano()
		}
		scene := skycloud.NewLive(seed)
		if cloudLoop > 0 {
			scene = skycloud.NewLoop(seed, int(cloudLoop.Seconds()*skycloud.StepRate))
		}
		scene.Mask.Bright = cloudBright

		screen, err := tcell.NewScreen()
		if err != nil {
			return fmt.Errorf("open the terminal: %w", err)
		}
		if err := screen.Init(); err != nil {
			return fmt.Errorf("open the terminal: %w", err)
		}
		defer screen.Fini()
		return canvas.RunCells(screen, scene, canvas.Options{FPS: skycloud.StepRate})
	}
	return cmd
}
